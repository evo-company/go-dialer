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
	if strings.Contains(m["Channel"], "test") {
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
}

func PhoneCallRecordStarter(m gami.Message) {
	// For one actual call we can have several bridge events depending on call type
	// So we need to remember channel for which we already started MixMonitor
	channel := m["Channel"]
	callsCache.Lock()
	defer callsCache.Unlock()
	if _, ok := callsCache.Map[channel]; ok {
		return
	}
	callsCache.Map[channel] = struct{}{}

	fileName := util.GetPhoneCallFileName(conf.GetConf().Name, m["Uniqueid"], "wav")
	fullFileName := fmt.Sprintf("%s/%s", conf.GetConf().FolderForCalls, fileName)
	res, err := ami.SendMixMonitor(m["Channel"], fullFileName)
	if err != nil {
		glog.Errorln(err)
	} else {
		glog.Infoln("MixMonitor sent...", res)
	}
}
