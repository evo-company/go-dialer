package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/go-dialer/ami"
	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/db"
	"github.com/warik/go-dialer/model"
	"github.com/warik/go-dialer/util"
)

var callsCache = util.NewSafeCallsCache()

// ============
// AMI handlers
// ============
func CdrEventHandler(m gami.Message) {
	innerPhoneNumber, opponentPhoneNumber, callType := util.GetPhoneDetails(m["Channel"],
		m["DestinationChannel"], m["Source"], m["Destination"], m["CallerID"])

	glog.Infoln("<<< INCOMING CDR", m)

	if callType == util.INNER_CALL || callType == -1 {
		glog.Errorln("<<< BAD CDR", callType)
		return
	}

	countryCode := util.GetCountryByPhones(innerPhoneNumber, opponentPhoneNumber)
	if _, ok := conf.GetConf().Agencies[countryCode]; countryCode == "" || !ok {
		glog.Errorln("Unexisting numbers...", innerPhoneNumber, opponentPhoneNumber,
			countryCode)
		return
	}
	if !util.IsNumbersValid(innerPhoneNumber, opponentPhoneNumber, countryCode) {
		glog.Errorln("Wrong numbers...", innerPhoneNumber, opponentPhoneNumber, countryCode)
		return
	}
	// We may receive time from asterisk with time zone offset
	// but for correct processing of it on portal side we need to store it in raw GMT
	m["StartTime"] = util.ConvertTime(m["StartTime"])
	m["InnerPhoneNumber"] = innerPhoneNumber
	m["OpponentPhoneNumber"] = opponentPhoneNumber
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
	if sec == 0 || m["Disposition"] != "ANSWERED" {
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

// =============
// REST handlers
// =============
func Stats(w http.ResponseWriter, r *http.Request) {
	page := `
		<h1>{{.Name}} dialer stats</h1>
		<b>DB CDR</b>: {{.DBCount}}
	`
	t, _ := template.New("stats").Parse(page)
	stats := model.DialerStats{conf.GetConf().Name, db.GetDB().GetCdrCount()}
	t.Execute(w, stats)
}

func CdrCount(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, model.Response{"number_of_cdrs": strconv.Itoa(db.GetDB().GetCdrCount())})
}

func DeleteCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	res, err := db.GetDB().DeleteCdr(cdr.Id)
	if err != nil {
		glog.Errorln(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else if count, _ := res.RowsAffected(); count != 1 {
		fmt.Fprint(w, model.Response{"status": "error", "message": "result is not 1"})
	} else {
		fmt.Fprint(w, model.Response{"status": "success"})
	}
}

func GetCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	returnCdr, err := db.GetDB().GetCDR(cdr.UniqueID)
	if err != nil {
		glog.Errorln(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, fmt.Sprintf("%#v", returnCdr))
	}
}

func ManagerCallAfterHours(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	settings := conf.GetConf().Agencies[phoneCall.Country]
	payload, _ := json.Marshal(model.Dict{
		"calling_phone": phoneCall.CallingPhone,
		"CompanyId":     settings.CompanyId,
	})
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_call_after_hours")
	resp, err := util.SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	util.APIResponseWriter(model.Response{"status": resp}, err, w)
}

func ShowCallingReview(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	settings := conf.GetConf().Agencies[phoneCall.Country]
	payload, _ := json.Marshal(model.Dict{
		"inner_number": phoneCall.InnerNumber,
		"review_href":  phoneCall.ReviewHref,
		"CompanyId":    settings.CompanyId,
	})
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_review_popup_to_manager")
	resp, err := util.SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	util.APIResponseWriter(model.Response{"status": resp}, err, w)
}

func ShowCallingPopup(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	settings := conf.GetConf().Agencies[phoneCall.Country]
	payload, _ := json.Marshal(model.Dict{
		"inner_number":  phoneCall.InnerNumber,
		"calling_phone": phoneCall.CallingPhone,
		"CompanyId":     settings.CompanyId,
	})
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_popup_to_manager")
	resp, err := util.SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	util.APIResponseWriter(model.Response{"status": resp}, err, w)
}

func ManagerPhoneForCompany(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	settings := conf.GetConf().Agencies[phoneCall.Country]
	payload, _ := json.Marshal(model.Dict{
		"id":        phoneCall.Id,
		"CompanyId": settings.CompanyId,
	})
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone_for_company")
	resp, err := util.SendRequest(payload, url, "GET", settings.Secret, settings.CompanyId)
	util.APIResponseWriter(model.Response{"inner_number": resp}, err, w)
}

func ManagerPhone(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	settings := conf.GetConf().Agencies[phoneCall.Country]
	payload, _ := json.Marshal(model.Dict{
		"calling_phone": phoneCall.CallingPhone,
		"CompanyId":     settings.CompanyId,
	})
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone")
	resp, err := util.SendRequest(payload, url, "GET", settings.Secret, settings.CompanyId)
	util.APIResponseWriter(model.Response{"inner_number": resp}, err, w)
}

func QueueRemove(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.PhoneCall))
	queue, err := ami.GetStaticQueue(call.InnerNumber)
	if err != nil {
		glog.Errorln(err)
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
		return
	}
	resp, err := ami.RemoveFromQueue(queue, call.Country, call.InnerNumber)
	util.AMIResponseWriter(w, resp, err, true, "Message")
}

func QueueAdd(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.PhoneCall))
	queue, err := ami.GetStaticQueue(call.InnerNumber)
	if err != nil {
		glog.Errorln(err)
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
		return
	}
	resp, err := ami.AddToQueue(queue, call.Country, call.InnerNumber)
	util.AMIResponseWriter(w, resp, err, true, "Message")
}

func PlaceSpy(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.Call))
	resp, err := ami.Spy(call)
	util.AMIResponseWriter(w, resp, err, true, "Message")
}

func ShowInuse(p interface{}, w http.ResponseWriter, r *http.Request) {
	resp, err := ami.GetActiveChannels()
	util.AMIResponseWriter(w, resp, err, true, "CmdData")
}

func PlaceCall(p interface{}, w http.ResponseWriter, r *http.Request) {
	resp, err := ami.Call(*p.(*model.Call))
	util.AMIResponseWriter(w, resp, err, true, "Message")
}

func PingAsterisk(w http.ResponseWriter, r *http.Request) {
	resp, err := ami.Ping()
	util.AMIResponseWriter(w, resp, err, true, "Ping")
}

func CheckPortals(w http.ResponseWriter, r *http.Request) {
	for country, settings := range conf.GetConf().Agencies {
		url := conf.GetConf().GetApi(country, "manager_phone")
		payload, _ := json.Marshal(model.Dict{
			"calling_phone": "6916",
			"CompanyId":     settings.CompanyId,
		})
		if _, err := util.SendRequest(payload, url, "GET", settings.Secret,
			settings.CompanyId); err != nil {
			fmt.Fprint(w, model.Response{"country": country, "error": err.Error()})
		} else {
			fmt.Fprint(w, model.Response{"country": country, "ok": "ok"})
		}
	}
}

func ImUp(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "Im up, Im up...")
}
