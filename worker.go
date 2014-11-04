package main

import (
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
)

func saveCdr(mChan <-chan gami.Message) {
	for len(mChan) > 0 {
		m := <-mChan
		settings := conf.GetConf().Agencies[m["CountryCode"]]
		url := conf.GetConf().GetApi(m["CountryCode"], "save_phone_call")
		_, err := SendRequest(m, url, "POST", settings.Secret, settings.CompanyId)
		if err == nil {
			glog.Infoln("<<< CDR SAVED", "|", m["UniqueID"])
			if err := db.GetDB().Delete([]byte(m["UniqueID"]), nil); err != nil {
				glog.Errorln("Error while deleting message - ", m["UniqueID"])
			}
		} else {
			glog.Errorln("<<< ERROR WHILE SAVING", "|", m["UniqueID"], err)
		}
	}
}

func readCdrs(mChan chan gami.Message) {
	iter := db.GetDB().NewIterator(nil, nil)
	readedCdrNum := 0
	for iter.Next() && readedCdrNum <= conf.MAX_CDR_NUMBER {
		m := gami.Message{}
		_ = json.Unmarshal(iter.Value(), &m)
		mChan <- m
		readedCdrNum++
	}
	for i := 0; i < min(readedCdrNum, conf.CDR_SAVERS_COUNT); i++ {
		go saveCdr(mChan)
	}
	glog.Infoln(fmt.Sprintf("<<< READING | PROCESS: %d", readedCdrNum))
	if readedCdrNum == conf.MAX_CDR_NUMBER {
		conf.Alert("Overload with cdr")
	}

	iter.Release()
	if err := iter.Error(); err != nil {
		glog.Errorln("Problem, while reading from db", err)
		conf.Alert("Problem, while reading from db")
	}
}

func CdrReader(wg *sync.WaitGroup, finishChan <-chan struct{}, ticker *time.Ticker) {
	glog.Infoln("Initiating CdrReader...")
	mChan := make(chan gami.Message, conf.MAX_CDR_NUMBER)
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing CdrReader...")
			ticker.Stop()
			wg.Done()
			return
		case <-ticker.C:
			getInnerNumbers()
			go readCdrs(mChan)
			glog.Flush()
		}
	}
}

func QueueManager(wg *sync.WaitGroup, queueTransport <-chan chan gami.Message, finishChan <-chan struct{},
	ticker *time.Ticker) {
	glog.Infoln("Initiating QueueManager...")
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing QueueManager...")
			ticker.Stop()
			wg.Done()
			return
		case <-ticker.C:
			glog.Infoln("<<< MANAGING QUEUES...")
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
								_, err := ami.RemoveFromQueue(queue, countryCode, number)
								if err != nil {
									glog.Errorln(err, number)
								}
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
			debug.FreeOSMemory()
		}
	}
}
