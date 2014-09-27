package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
)

func CdrReader(wg *sync.WaitGroup, cdrChan chan<- gami.Message, finishChan <-chan struct{},
	ticker *time.Ticker) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing CdrReader")
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

				glog.Infoln(fmt.Sprintf("Reading data. Total-%s;Processing-%s ",
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

func CdrSaver(wg *sync.WaitGroup, cdrChan <-chan gami.Message, finishChan <-chan struct{},
	i int) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing CdrSaver", strconv.Itoa(i))
			wg.Done()
			return
		case m := <-cdrChan:
			glog.Infoln("Processing message -", m["UniqueID"])

			settings := conf.GetConf().Agencies[m["CountryCode"]]
			url := conf.GetConf().GetApi(m["CountryCode"], "save_phone_call")
			_, err := SendRequest(m, url, "POST", settings.Secret, settings.CompanyId)
			if err == nil {
				db.DeleteChan <- m
				break
			}
		}
	}
}

func DbHandler(wg *sync.WaitGroup, finishChan <-chan struct{}) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing DbHandler")
			wg.Done()
			return
		case m := <-db.DeleteChan:
			_ = db.GetDB().Update(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
				if err := b.Delete([]byte(m["UniqueID"])); err != nil {
					glog.Infoln("Error while deleting message - ", m["UniqueID"])
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

func QueueManager(wg *sync.WaitGroup, finishChan <-chan struct{}, ticker *time.Ticker) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing QueueManager")
			wg.Done()
			return
		case <-ticker.C:
			getInnerNumbers()
			_ = ami.GetAMI().SendAction(gami.Message{"Action": "QueueStatus"}, nil)
			for countryCode, settings := range conf.GetConf().Agencies {
				queuestStates := map[string]string{}
				for number, _ := range InnerPhonesNumber[countryCode] {
					cb, cbc := GetCallback()
					command := fmt.Sprintf("database get %s %s", "queues/u2q", number)
					if err := ami.GetAMI().Command(command, &cb); err != nil {
						glog.Errorln(err)
						continue
					}
					resp := <-cbc
					val, ok := resp["Value"]
					if !ok {
						// No static queue for such number - skip
						continue
					}
					glog.Infoln(settings, val)
				}
				url := conf.GetConf().GetApi(countryCode, "save_queues_state")
				_, err := SendRequest(queuestStates, url, "POST", settings.Secret,
					settings.CompanyId)
				if err != nil {
					glog.Errorln(err)
				}
			}
		}
	}
}

func CdrEventHandler(m gami.Message) {
	innerPhoneNumber, opponentPhoneNumber, callType := GetPhoneDetails(m)
	if callType != INNER_CALL && callType != -1 {
		countryCode := ""
		innerPhones := InnerPhonesNumber
		for country, numbers := range innerPhones {
			if _, ok := numbers[innerPhoneNumber]; ok {
				countryCode = country
				break
			}
		}
		if countryCode != "" {
			m["InnerPhoneNumber"] = innerPhoneNumber
			m["OpponentPhoneNumber"] = opponentPhoneNumber
			m["CallType"] = strconv.Itoa(callType)
			m["CountryCode"] = countryCode
			glog.Infoln("Reading message -", m["UniqueID"])
			glog.Infoln(m)
			db.PutChan <- m
		} else {
			glog.Errorln("Unexisting numbers...", innerPhoneNumber, opponentPhoneNumber)
		}
	}
}
