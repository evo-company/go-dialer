package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/kr/pretty"
	"github.com/parnurzeal/gorequest"
	"github.com/vmihailenco/signer"
	"github.com/warik/gami"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/dialer/model"
)

func min(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

func signData(m map[string]string, secret string) (signedData string, err error) {
	h := hmac.New(func() hash.Hash {
		return sha1.New()
	}, []byte(secret))

	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	signedData = string(signer.NewBase64Signer(h).Sign(data))
	return
}

func UnsignData(i interface{}, d model.SignedInputData) error {
	h := hmac.New(func() hash.Hash {
		return sha1.New()
	}, []byte(conf.GetConf().Agencies[d.Country].Secret))
	dataString, ok := signer.NewBase64Signer(h).Verify([]byte(d.Data))
	if !ok {
		return errors.New("Bad signature")
	}

	return json.Unmarshal(dataString, &i)
}

func Clean(finishChannels []chan struct{}, wg *sync.WaitGroup) {
	for _, channel := range finishChannels {
		close(channel)
	}
	wg.Wait()
	db.GetDB().Close()
	ami.GetAMI().Logoff()
	// panic("Manual panic for checking goroutines")
}

func SendRequest(m map[string]string, url, method, secret, companyId string) (string, error) {
	m["CompanyId"] = companyId
	signedData, err := signData(m, secret)
	if err != nil {
		return "", err
	}

	data := model.SignedData{Data: signedData, CompanyId: companyId}
	request := gorequest.New()
	if method == "POST" {
		request.Post(url).Send(data)
	} else if method == "GET" {
		query, _ := json.Marshal(data)
		request.Get(url).Query(string(query))
	}

	resp, respBody, errs := request.Timeout(conf.REQUEST_TIMEOUT * time.Second).End()

	if len(errs) != 0 {
		return "", errs[0]
	}

	if resp.StatusCode != 200 {
		return "", errors.New(fmt.Sprintf(conf.REMOTE_ERROR_TEXT, resp.StatusCode))
	}

	return respBody, nil
}

func CdrReader(wg *sync.WaitGroup, cdrChan chan<- gami.Message, finishChan <-chan struct{},
	ticker *time.Ticker) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing cdrReader")
			ticker.Stop()
			wg.Done()
			return
		case <-ticker.C:
			_ = db.GetDB().View(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
				totalCdrNum := b.Stats().KeyN
				readedCdrNum := 0

				if totalCdrNum >= conf.MAX_CDR_NUMBER {
					conf.Alert(fmt.Sprintf("Too much cdrs - %s", strconv.Itoa(totalCdrNum)))
				}

				pretty.Log(fmt.Sprintf("Reading data. Total-%s;Processing-%s ",
					strconv.Itoa(totalCdrNum),
					strconv.Itoa(min(totalCdrNum, conf.MAX_CDR_NUMBER))))
				c := b.Cursor()
				for k, v := c.First(); k != nil && readedCdrNum <= conf.MAX_CDR_NUMBER; k, v = c.Next() {
					m := gami.Message{}
					_ = json.Unmarshal(v, &m)
					cdrChan <- m
					readedCdrNum++
				}
				return nil
			})
		}
	}
}

func CdrHandler(wg *sync.WaitGroup, cdrChan <-chan gami.Message, finishChan <-chan struct{},
	i int) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing cdrHandler", strconv.Itoa(i))
			wg.Done()
			return
		case m := <-cdrChan:
			pretty.Log("Processing message -", m["UniqueID"])
			for countryCode, settings := range conf.GetConf().Agencies {
				url := conf.GetConf().GetApi(countryCode, "save_phone_call")
				_, err := SendRequest(m, url, "POST", settings.Secret, settings.CompanyId)
				if err == nil {
					db.DeleteChan <- m
					break
				}
			}
		}
	}
}

func DbHandler(wg *sync.WaitGroup, finishChan <-chan struct{}) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing dbHandler")
			wg.Done()
			return
		case m := <-db.DeleteChan:
			_ = db.GetDB().Update(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
				if err := b.Delete([]byte(m["UniqueID"])); err != nil {
					pretty.Log("Error while deleting message - ", m["UniqueID"])
				}
				return nil
			})
		case m := <-db.PutChan:
			_ = db.GetDB().Update(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
				value, _ := json.Marshal(m)
				if err := b.Put([]byte(m["UniqueID"]), value); err != nil {
					conf.Alert("Cannot add cdr to db")
					panic(err)
				}
				return nil
			})
		}
	}
}
