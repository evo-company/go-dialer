package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/kr/pretty"
	"github.com/warik/gami"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/dialer/model"
)

func DBStats(w http.ResponseWriter, r *http.Request) {
	data, _ := json.Marshal(db.GetStats())
	fmt.Fprint(w, string(data))
}

func CdrNumber(w http.ResponseWriter, r *http.Request) {
	db.GetDB().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
		fmt.Fprint(w, model.Response{"number_of_cdrs": strconv.Itoa(b.Stats().KeyN)})
		return nil
	})
}

func DeleteCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	err := db.GetDB().Update(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
		err = b.Delete([]byte(cdr.Id))
		return
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": "success"})
	}
}

func GetCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
	cdr := (*p.(*model.Cdr))
	var returnCdr string
	err := db.GetDB().View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
		returnCdr = string(b.Get([]byte(cdr.Id)))
		return
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, returnCdr)
	}
}

func ManagerCallAfterHours(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_call_after_hours")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": resp})
	}
}

func ShowCallingReview(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{
		"inner_number": phoneCall.InnerNumber,
		"review_href":  phoneCall.ReviewHref,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_review_popup_to_manager")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": resp})
	}
}

func ShowCallingPopup(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{
		"inner_number":  phoneCall.InnerNumber,
		"calling_phone": phoneCall.CallingPhone,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_popup_to_manager")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": resp})
	}
}

func ManagerPhoneForCompany(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"id": phoneCall.Id}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone_for_company")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"inner_number": resp})
	}
}

func ManagerPhone(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings.Secret, settings.CompanyId)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"inner_number": resp})
	}
}

func DBGet(p interface{}, w http.ResponseWriter, r *http.Request) {
	dbGetter := (*p.(*model.DbGetter))
	cb, cbc := GetCallback()
	command := fmt.Sprintf("database get %s %s", dbGetter.Family, dbGetter.Key)
	if err := ami.GetAMI().Command(command, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, false, "Value")
	}
}

func QueueStatus(p interface{}, w http.ResponseWriter, r *http.Request) {
	cb, cbc := GetCallback()
	m := gami.Message{"Action": "QueueStatus"}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, true, "Message")
	}
}

func QueueRemove(p interface{}, w http.ResponseWriter, r *http.Request) {
	queue := (*p.(*model.Queue))
	cb, cbc := GetCallback()
	m := gami.Message{"Action": "QueueRemove", "Queue": queue.Queue, "Interface": queue.Interface}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, true, "Message")
	}
}

func QueueAdd(p interface{}, w http.ResponseWriter, r *http.Request) {
	queue := (*p.(*model.Queue))
	cb, cbc := GetCallback()
	m := gami.Message{
		"Action":         "QueueAdd",
		"Queue":          queue.Queue,
		"Interface":      queue.Interface,
		"StateInterface": queue.StateInterface,
	}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, true, "Message")
	}
}

func PlaceSpy(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.Call))
	o := gami.NewOriginateApp(call.GetChannel(), "ChanSpy", fmt.Sprintf("SIP/%v", call.Exten))
	o.Async = true

	cb, cbc := GetCallback()
	if err := ami.GetAMI().Originate(o, nil, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, true, "Message")
	}
}

func ShowChannels(p interface{}, w http.ResponseWriter, r *http.Request) {
	cb, cbc := GetCallback()
	if err := ami.GetAMI().Command("sip show inuse", &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, false, "CmdData")
	}
}

func ShowInuse(p interface{}, w http.ResponseWriter, r *http.Request) {
	cb, cbc := GetCallback()
	if err := ami.GetAMI().Command("sip show inuse", &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, false, "CmdData")
	}
}

func PlaceCall(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.Call))

	o := gami.NewOriginate(call.GetChannel(), "", strings.TrimPrefix(call.Exten, "+"), "1")
	o.CallerID = call.GetCallerID()
	o.Async = true

	cb, cbc := GetCallback()
	if err := ami.GetAMI().Originate(o, nil, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, true, "Message")
	}
}

func PingAsterisk(w http.ResponseWriter, r *http.Request) {
	cb, cbc := GetCallback()
	if err := ami.GetAMI().SendAction(gami.Message{"Action": "Ping"}, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		WriteResponse(w, <-cbc, true, "Ping")
	}
}

func ImUp(w http.ResponseWriter, r *http.Request) {
	pretty.Println("Im up, Im up...")
}
