package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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

				glog.Infoln(fmt.Sprintf("<<< READING. TOTAL-%s;PROCESS-%s ",
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
			glog.Infoln("<<< PROCESSING MSG", m["UniqueID"])

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
					glog.Errorln("Error while deleting message - ", m["UniqueID"])
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

func QueueManager(wg *sync.WaitGroup, queueTransport <-chan chan gami.Message, finishChan <-chan struct{},
	ticker *time.Ticker) {
	wg.Add(1)
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing QueueManager")
			ticker.Stop()
			wg.Done()
			return
		case <-ticker.C:
			glog.Infoln("QueueManager starts to check...")
			getInnerNumbers()
			// Send queue status to AMI
			if err := ami.QueueStatus(); err != nil {
				glog.Errorln(err)
				return
			}
			// and wait for channel with active queues from asterisk
			activeQueuesChan := <-queueTransport
			// sort active queues for each number per country
			queuesNumberMap := GetActiveQueuesMap(activeQueuesChan)
			for countryCode, settings := range conf.GetConf().Agencies {
				tqs := queuesNumberMap[countryCode]
				numbersState := make(Dict)
				// For each inner number get its static queue from asterisk db
				for number, _ := range InnerPhonesNumber[countryCode] {
					staticQueue, err := ami.GetStaticQueue(number)
					if err != nil {
						// if there is no static queue for number - some problem with it, skip
						continue
					}
					staticQueue = strings.Split(staticQueue, "\n")[0]
					// if there is no active queues for such number, then its not available
					if _, ok := tqs[number]; !ok {
						numbersState[number] = "not_available"
					} else {
						// if there are some, which are not its static queue and not general queue
						// (same as static but without last digit)
						// then number should be removed from them and still not available
						status := "not_available"
						for _, queue := range tqs[number] {
							generalizedQueue := staticQueue[:len(staticQueue)-1]
							if staticQueue == queue || generalizedQueue == queue {
								status = "available"
							} else {
								// _, err := ami.RemoveFromQueue(queue, countryCode, number)
								// if err != nil {
								// 	glog.Errorln(err, number)
								// }
							}
						}
						numbersState[number] = status
					}
				}
				url := conf.GetConf().GetApi(countryCode, "save_company_queues_states")
				_, err := SendRequest(numbersState, url, "POST", settings.Secret,
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
			glog.Infoln("<<< READING MSG", m["UniqueID"])
			glog.Infoln(m)
			db.PutChan <- m
		} else {
			glog.Errorln("Unexisting numbers...", innerPhoneNumber, opponentPhoneNumber)
		}
	}
}
