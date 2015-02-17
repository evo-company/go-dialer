package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/dialer/model"
	"github.com/warik/dialer/util"
)

var callsCache = util.NewSafeCallsCache()

// ============
// AMI handlers
// ============
func CdrEventHandler(m gami.Message) {
	innerPhoneNumber, opponentPhoneNumber, callType := util.GetPhoneDetails(m["Channel"],
		m["DestinationChannel"], m["Source"], m["Destination"], m["CallerID"])
	if callType != util.INNER_CALL && callType != -1 {
		countryCode := util.GetCountryByInnerPhone(innerPhoneNumber)
		if countryCode == "" {
			glog.Errorln("Unexisting numbers...", innerPhoneNumber, opponentPhoneNumber)
			return
		}
		// We may receive time from asterisk with time zone offset
		// but for correct processing of it on portal side we need to store it in raw GMT
		m["StartTime"] = util.ConvertTime(m["StartTime"], countryCode)
		m["InnerPhoneNumber"] = innerPhoneNumber
		m["OpponentPhoneNumber"] = opponentPhoneNumber
		m["CallType"] = strconv.Itoa(callType)
		m["CountryCode"] = countryCode
		m["CompanyId"] = conf.GetConf().Agencies[countryCode].CompanyId
		glog.Infoln("<<< READING MSG", m["UniqueID"])
		glog.Infoln(m)

		if err := db.GetDB().AddCDR(m); err != nil {
			conf.Alert("Cannot add cdr to db")
			panic(err)
		}
	}
}

func PhoneCallsHandler(m gami.Message) {
	uniqueId := m["Uniqueid1"]
	callsCache.Lock()
	defer callsCache.Unlock()
	if _, ok := callsCache.Map[uniqueId]; ok {
		return
	}
	callsCache.Map[uniqueId] = struct{}{}

	// If Channel2 field has inner number then its incoming call and we need to show popup
	// t := util.PHONE_RE.FindStringSubmatch(m["Channel2"])
	// if t != nil {
	// 	innerPhoneNumber := t[1]
	// 	country := util.GetCountryByInnerPhone(innerPhoneNumber)
	// 	opponentPhoneNumber := m["CallerID1"]
	// 	if country == "" {
	// 		glog.Errorln("Unexisting numbers...", innerPhoneNumber, opponentPhoneNumber)
	// 		return
	// 	}
	// 	glog.Infoln(fmt.Sprintf("Showing popup to: %s, %s", innerPhoneNumber, country))
	// 	if err := util.ShowCallingPopup(innerPhoneNumber, opponentPhoneNumber,
	// 		country); err != nil {
	// 		glog.Errorln(err)
	// 	}
	// }

	if *savePhoneCalls {
		fileName := fmt.Sprintf("%s/%s-%s.wav", conf.GetConf().FolderForCalls, conf.GetConf().Name,
			uniqueId)
		if _, err := ami.SendMixMonitor(m["Channel1"], fileName); err != nil {
			glog.Errorln(err)
		}
	}
}

// =============
// REST handlers
// =============
func DBStats(w http.ResponseWriter, r *http.Request) {
	// fmt.Fprint(w, db.GetStats())
}

func CdrNumber(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, model.Response{"number_of_cdrs": strconv.Itoa(db.GetDB().GetCount())})
}

func DeleteCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	err := db.GetDB().Delete(cdr.Id)
	if err != nil {
		glog.Errorln(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": "success"})
	}
}

func GetCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	returnCdr, err := db.GetDB().GetCDR(cdr.Id)
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
