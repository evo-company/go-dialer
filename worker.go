package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/go-dialer/ami"
	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/db"
	"github.com/warik/go-dialer/model"
	"github.com/warik/go-dialer/util"
)

func CdrSaver(wg *sync.WaitGroup, mChan <-chan db.CDR, finishChan <-chan struct{}) {
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing CdrSaver...")
			wg.Done()
			return
		case cdr := <-mChan:
			settings := conf.GetConf().Agencies[cdr.CountryCode]
			url := conf.GetConf().GetApi(cdr.CountryCode, "save_phone_call")
			data, _ := json.Marshal(cdr)
			_, err := util.SendRequest(data, url, "POST", settings.Secret, settings.CompanyId)
			if err == nil {
				glog.Infoln("<<< CDR SAVED", "|", cdr.UniqueID)
				res, err := db.GetDB().Delete(cdr.UniqueID)
				if err != nil {
					glog.Errorln("Error while deleting message - ", cdr.UniqueID, err)
				} else if count, _ := res.RowsAffected(); count != 1 {
					glog.Errorln("CDR was not deleted - ", cdr.UniqueID)
				} else {
					glog.Errorln("<<< ERROR WHILE SAVING", "|", cdr.UniqueID, err)
				}
			}
		}
	}
}

func CdrReader(wg *sync.WaitGroup, mChan chan<- db.CDR, finishChan <-chan struct{},
	ticker *time.Ticker) {
	glog.Infoln("Initiating CdrReader...")
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing CdrReader...")
			ticker.Stop()
			wg.Done()
			return
		case <-ticker.C:
			cdrs := db.GetDB().SelectCDRs(conf.MAX_CDR_NUMBER)
			for _, cdr := range cdrs {
				mChan <- cdr
			}

			cdrsReaded := len(cdrs)
			glog.Infoln(fmt.Sprintf("<<< READING | PROCESS: %d", cdrsReaded))
			if cdrsReaded == conf.MAX_CDR_NUMBER {
				conf.Alert(fmt.Sprintf("Overload with cdr, %d", db.GetDB().GetCount()))
			}

			glog.Flush()
		}
	}
}

func NumbersLoader(wg *sync.WaitGroup, finishChan <-chan struct{}, ticker *time.Ticker) {
	glog.Infoln("Initiating NumbersLoader...")
	for {
		select {
		case <-finishChan:
			glog.Warningln("Finishing NumbersLoader...")
			ticker.Stop()
			wg.Done()
			return
		case <-ticker.C:
			util.InnerPhonesNumbers.LoadInnerNumbers()
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
			util.InnerPhonesNumbers.RLock()
			glog.Infoln("<<< MANAGING QUEUES...")
			// Send queue status to AMI
			if err := ami.QueueStatus(); err != nil {
				glog.Errorln(err)
				return
			}
			// and wait for channel with active queues from asterisk
			activeQueuesChan := <-queueTransport
			// pre-initialize map for numbers state per each country
			queuesNumberMap := util.GetActiveQueuesMap(activeQueuesChan)
			numbersStateMap := map[string]model.Dict{}
			for countryCode, _ := range conf.GetConf().Agencies {
				numbersStateMap[countryCode] = model.Dict{}
			}
			// For each inner number get its static queue from asterisk db
			for number, countryCode := range util.InnerPhonesNumbers.UniqueNumbersMap {
				staticQueue, err := ami.GetStaticQueue(number)
				if err != nil {
					// if there is no static queue for number - some problem with it, skip
					continue
				}
				staticQueue = strings.Split(staticQueue, "\n")[0]
				// if there is no active queues for such number, then its not available
				if _, ok := queuesNumberMap[number]; !ok {
					numbersStateMap[countryCode][number] = "not_available"
				} else {
					// if there are some, which are not its static queue and not general queue
					// (same as static but without last digit)
					// then number should be removed from them and still not available
					status := "not_available"
					for _, queue := range queuesNumberMap[number] {
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
					numbersStateMap[countryCode][number] = status
				}
			}
			for countryCode, numbersState := range numbersStateMap {
				url := conf.GetConf().GetApi(countryCode, "save_company_queues_states")
				payload, _ := json.Marshal(numbersState)
				settings := conf.GetConf().Agencies[countryCode]
				_, err := util.SendRequest(payload, url, "POST", settings.Secret,
					settings.CompanyId)
				if err != nil {
					glog.Errorln(err)
				}
			}
			util.InnerPhonesNumbers.RUnlock()
		}
	}
}
