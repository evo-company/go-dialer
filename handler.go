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
)

// ============
// AMI handlers
// ============
func CdrEventHandler(m gami.Message) {
	innerPhoneNumber, opponentPhoneNumber, callType := GetPhoneDetails(m["Channel"],
		m["DestinationChannel"], m["Source"], m["Destination"], m["CallerID"])
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

			value, _ := json.Marshal(m)
			if err := db.GetDB().Put([]byte(m["UniqueID"]), value, nil); err != nil {
				conf.Alert("Cannot add cdr to db")
				panic(err)
			}
		} else {
			glog.Errorln("Unexisting numbers...", innerPhoneNumber, opponentPhoneNumber)
		}
	}
}

func PhoneCallsHandler(m gami.Message) {
	suffix := ""
	if m["Event"] == "Bridge" {
		suffix = "1"
	}
	channel := m["Channel"+suffix]
	fileName := fmt.Sprintf("%s/%s.wav", conf.FOLDER_FOR_CALLS, m["Uniqueid"]+suffix)
	if _, err := ami.SendMixMonitor(channel, fileName); err != nil {
		glog.Errorln(err)
	}
}

// =============
// REST handlers
// =============
func DBStats(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, db.GetStats())
}

func CdrNumber(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, model.Response{"number_of_cdrs": strconv.Itoa(db.GetCount())})
}

func DeleteCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	err := db.GetDB().Delete([]byte(cdr.Id), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": "success"})
	}
}

func GetCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	returnCdr, err := db.GetDB().Get([]byte(cdr.Id), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, string(returnCdr))
	}
}

func ManagerCallAfterHours(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := Dict{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_call_after_hours")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	APIResponseWriter(model.Response{"status": resp}, err, w)
}

func ShowCallingReview(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := Dict{
		"inner_number": phoneCall.InnerNumber,
		"review_href":  phoneCall.ReviewHref,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_review_popup_to_manager")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	APIResponseWriter(model.Response{"status": resp}, err, w)
}

func ShowCallingPopup(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := Dict{
		"inner_number":  phoneCall.InnerNumber,
		"calling_phone": phoneCall.CallingPhone,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_popup_to_manager")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	APIResponseWriter(model.Response{"status": resp}, err, w)
}

func ManagerPhoneForCompany(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := Dict{"id": phoneCall.Id}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone_for_company")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "GET", settings.Secret, settings.CompanyId)
	APIResponseWriter(model.Response{"inner_number": resp}, err, w)
}

func ManagerPhone(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := Dict{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "GET", settings.Secret, settings.CompanyId)
	APIResponseWriter(model.Response{"inner_number": resp}, err, w)
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
	AMIResponseWriter(w, resp, err, true, "Message")
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
	AMIResponseWriter(w, resp, err, true, "Message")
}

func PlaceSpy(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.Call))
	resp, err := ami.Spy(call)
	AMIResponseWriter(w, resp, err, true, "Message")
}

func ShowInuse(p interface{}, w http.ResponseWriter, r *http.Request) {
	resp, err := ami.GetActiveChannels()
	AMIResponseWriter(w, resp, err, true, "CmdData")
}

func PlaceCall(p interface{}, w http.ResponseWriter, r *http.Request) {
	resp, err := ami.Call(*p.(*model.Call))
	AMIResponseWriter(w, resp, err, true, "Message")
}

func PingAsterisk(w http.ResponseWriter, r *http.Request) {
	resp, err := ami.Ping()
	AMIResponseWriter(w, resp, err, true, "Ping")
}

func CheckPortals(w http.ResponseWriter, r *http.Request) {
	for country, settings := range conf.GetConf().Agencies {
		url := conf.GetConf().GetApi(country, "manager_phone")
		if _, err := SendRequest(Dict{"calling_phone": "6916"}, url, "GET", settings.Secret,
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
