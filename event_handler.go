package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"github.com/warik/gami"
	"github.com/warik/go-dialer/ami"
	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/db"
	"github.com/warik/go-dialer/util"
)

var callsCache = util.NewSafeMap()

func CdrEventHandler(m gami.Message) {
	glog.Infoln("<<< INCOMING CDR", m)

	var innerNumber, outerNumber string
	var callType int

	// Callback cdr needed to be processed in another way because we actually
	// need to collect all required info from 2 separated cdrs
	if strings.Contains(m["Channel"], conf.GetConf().GetCallBackQueueSufix()) {
		innerNumber, outerNumber, callType = util.GetCallBackPhoneDetails(
			m["Channel"], m["Destination"], m["DestinationChannel"])
	} else {
		innerNumber, outerNumber, callType = util.GetPhoneDetails(
			m["Channel"], m["DestinationChannel"], m["Source"],
			m["Destination"], m["CallerID"])
	}

	if callType == util.INNER_CALL || callType == -1 {
		glog.Errorln("<<< BAD CDR", callType)
		return
	}

	countryCode := util.GetCountryByPhones(innerNumber, outerNumber)
	if _, ok := conf.GetConf().Agencies[countryCode]; countryCode == "" || !ok {
		glog.Errorln("Unexisting numbers...", innerNumber, outerNumber,
			countryCode)
		return
	}

	if !util.IsNumbersValid(innerNumber, outerNumber, countryCode) {
		glog.Errorln("Wrong numbers...", innerNumber, outerNumber, countryCode)
		return
	}
	// We may receive time from asterisk with time zone offset
	// but for correct processing of it on portal side we need to store it in raw GMT
	m["StartTime"] = util.ConvertTime(m["StartTime"])
	m["InnerPhoneNumber"] = innerNumber
	m["OpponentPhoneNumber"] = outerNumber
	m["CallType"] = strconv.Itoa(callType)
	m["CountryCode"] = countryCode
	m["CompanyId"] = conf.GetConf().Agencies[countryCode].CompanyId

	_, err := db.GetDB().AddCDR(m)
	if err != nil {
		conf.Alert(err.Error())
		glog.Errorln(err)
	}
	glog.Infoln("<<< SAVED CDR", m["UniqueID"])

	// For future phone call record convertion and sending we need to be sure
	// that it exists
	sec, _ := strconv.Atoi(m["BillableSeconds"])
	badCallFromCRM := strings.Contains(m["CallerID"], "call_from_CRM") && sec <= 10
	if sec == 0 || m["Disposition"] != "ANSWERED" || badCallFromCRM {
		return
	}

	_, err = db.GetDB().AddPhoneCall(m["UniqueID"])
	if err != nil {
		conf.Alert(err.Error())
		glog.Errorln(err)
	}

	// Also after each successful call we need to show manager popup with
	// call quality review
	reviewHref := conf.GetConf().GetReviewUri(m["UniqueID"])
	resp, err := util.ShowReviewPopup(reviewHref, innerNumber, countryCode)
	if err != nil {
		glog.Errorln(err)
	} else {
		glog.Infoln("Show review popup response", resp)
	}
}

func BridgeEventHandler(m gami.Message) {
	// For each call we have at least 2 bridging events with different channels
	// But for showing popup we need exactly second one "BridgeNumChannels: 2"
	// And by check that ConnectedLineNum have length more than 4 digits
	// we can tell that this is incoming call (because cally number is big)
	callingPhone := m["ConnectedLineNum"]
	if *showPopups && m["BridgeNumChannels"] == "2" && len(callingPhone) > 4 {
		innerNumberArr := util.PHONE_RE.FindStringSubmatch(m["Channel"])
		if innerNumberArr == nil {
			glog.Errorln("Bad bridge event", m)
			return
		}
		innerNumber := innerNumberArr[1]
		country := util.GetCountryByPhones(innerNumber, callingPhone)
		if country == "" {
			glog.Errorln("Unexisting numbers...", innerNumber, callingPhone)
		}
		resp, err := util.ShowCallingPopup(innerNumber, callingPhone, country)
		if err != nil {
			glog.Errorln(err)
		} else {
			glog.Infoln("Show calling popup response", resp)
		}
	}

	// For one actual call we can have several bridge events depending on call type
	// So we need to remember channel for which we already started MixMonitor
	// callsCache.Lock()
	// defer callsCache.Unlock()
	// channel := m["BridgeUniqueid"]
	// if _, ok := callsCache.Map[channel]; ok {
	// 	return
	// }
	// callsCache.Map[channel] = struct{}{}
	glog.Infoln(m)
	if *savePhoneCalls && m["BridgeNumChannels"] == "1" {
		fileName := util.GetPhoneCallFileName(conf.GetConf().Name, m["Uniqueid"], "wav")
		fullFileName := fmt.Sprintf("%s/%s", conf.GetConf().FolderForCalls, fileName)
		_, err := ami.SendMixMonitor(m["Channel"], fullFileName)
		if err != nil {
			glog.Errorln(err)
		} else {
			glog.Infoln("MixMonitor sent...", fileName)
		}
	}
}
