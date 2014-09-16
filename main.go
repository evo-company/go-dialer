package main

import (
	"fmt"
	"net/http"
	// _ "net/http/pprof"
	"strconv"
	"strings"
	"time"

	"github.com/boltdb/bolt"
	"github.com/kr/pretty"
	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/dialer/model"
	"github.com/warik/gami"
	"github.com/zenazn/goji"
	"github.com/zenazn/goji/graceful"
)

func getCallback() (cb func(gami.Message), cbc chan gami.Message) {
	cbc = make(chan gami.Message)
	cb = func(m gami.Message) {
		pretty.Log("Handling response...")
		cbc <- m
	}
	return
}

func writeResponse(
	w http.ResponseWriter,
	resp gami.Message,
	statusFromResponse bool,
	dataKey string,
) {
	var status string
	if statusFromResponse {
		status = strings.ToLower(resp["Response"])
	} else {
		status = "success"
	}
	fmt.Fprint(w, model.Response{"status": status, "response": resp[dataKey]})
}

func main() {
	finishChannels := []chan struct{}{make(chan struct{})}
	ami.RegisterHandler("Cdr", finishChannels[len(finishChannels)-1])

	cdrChan := make(chan gami.Message)
	finishChannels = append(finishChannels, make(chan struct{}))
	ticker := time.NewTicker(conf.CDR_READ_INTERVAL)
	go CdrReader(cdrChan, finishChannels[len(finishChannels)-1], ticker)

	for i := 0; i < conf.HANDLERS_COUNT; i++ {
		finishChannels = append(finishChannels, make(chan struct{}))
		go CdrHandler(cdrChan, finishChannels[len(finishChannels)-1], i)
	}

	finishChannels = append(finishChannels, make(chan struct{}))
	go DbHandler(finishChannels[len(finishChannels)-1])

	initRoutes()
	graceful.PostHook(func() {
		Clean(finishChannels)
	})
	goji.Serve()
}

func initRoutes() {
	// API for self
	goji.Get("/", imUp)
	goji.Get("/ping-asterisk", pingAsterisk)
	goji.Get("/cdr_number", cdrNumber)
	goji.Post("/delete_cdr", withStructParams(new(model.Cdr), deleteCdr))

	//API for prom
	goji.Get("/show_inuse", withSignedParams(new(model.DummyStruct), showInuse))
	goji.Get("/show_channels", withSignedParams(new(model.DummyStruct), showChannels))
	goji.Get("/queue_status", withSignedParams(new(model.DummyStruct), queueStatus))
	goji.Get("/db_get", withSignedParams(new(model.DbGetter), dbGet))
	goji.Post("/call", withSignedParams(new(model.Call), placeCall))
	goji.Post("/spy", withSignedParams(new(model.Call), placeSpy))
	goji.Post("/queue_add", withSignedParams(new(model.Queue), queueAdd))
	goji.Post("/queue_remove", withSignedParams(new(model.Queue), queueRemove))

	// API for asterisk
	goji.Get("/manager_phone", withStructParams(new(model.PhoneCall), managerPhone))
	goji.Get("/manager_phone_for_company",
		withStructParams(new(model.PhoneCall), managerPhoneForCompany))
	goji.Get("/show_calling_popup_to_manager",
		withStructParams(new(model.PhoneCall), showCallingPopup))
	goji.Get("/show_calling_review_popup_to_manager",
		withStructParams(new(model.PhoneCall), showCallingReview))
	goji.Get("/manager_call_after_hours",
		withStructParams(new(model.PhoneCall), managerCallAfterHours))

	goji.Use(JSONReponse)
	goji.Use(AllowedRemoteAddress)
}

func JSONReponse(h http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "json")
		h.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func AllowedRemoteAddress(h http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		for _, addr := range conf.GetConf().AllowedRemoteAddrs {
			if addr == strings.Split(r.RemoteAddr, ":")[0] {
				h.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "Not allowed remote address", http.StatusUnauthorized)
	}
	return http.HandlerFunc(fn)
}

func withSignedParams(i interface{}, h func(interface{}, http.ResponseWriter,
	*http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		signedData := new(model.SignedInputData)
		if err := model.GetStructFromParams(r, signedData); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := UnsignData(i, (*signedData)); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		h(i, w, r)
	}
}

func withStructParams(i interface{}, h func(interface{}, http.ResponseWriter,
	*http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := model.GetStructFromParams(r, i); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h(i, w, r)
	}
}

func cdrNumber(w http.ResponseWriter, r *http.Request) {
	db.GetDB().View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(conf.BOLT_CDR_BUCKET))
		if b == nil {
			fmt.Fprint(w, model.Response{"status": "no such bucket"})
		} else {
			fmt.Fprint(w, model.Response{"number_of_cdrs": strconv.Itoa(b.Stats().KeyN)})
		}
		return nil
	})
}

func deleteCdr(p interface{}, w http.ResponseWriter, r *http.Request) {
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

func managerCallAfterHours(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_call_after_hours")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings["secret"], settings["companyId"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": resp})
	}
}

func showCallingReview(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{
		"inner_number": phoneCall.InnerNumber,
		"review_href":  phoneCall.ReviewHref,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_review_popup_to_manager")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings["secret"], settings["companyId"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": resp})
	}
}

func showCallingPopup(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{
		"inner_number":  phoneCall.InnerNumber,
		"calling_phone": phoneCall.CallingPhone,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_popup_to_manager")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings["secret"], settings["companyId"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"status": resp})
	}
}

func managerPhoneForCompany(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"id": phoneCall.Id}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone_for_company")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings["secret"], settings["companyId"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"inner_number": resp})
	}
}

func managerPhone(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone")
	settings := conf.GetConf().Agencies[phoneCall.Country]
	resp, err := SendRequest(payload, url, "POST", settings["secret"], settings["companyId"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, model.Response{"inner_number": resp})
	}
}

func dbGet(p interface{}, w http.ResponseWriter, r *http.Request) {
	dbGetter := (*p.(*model.DbGetter))
	cb, cbc := getCallback()
	command := fmt.Sprintf("database get %s %s", dbGetter.Family, dbGetter.Key)
	if err := ami.GetAMI().Command(command, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, false, "CmdData")
	}
}

func queueStatus(p interface{}, w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	m := gami.Message{"Action": "QueueStatus"}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Message")
	}
}

func queueRemove(p interface{}, w http.ResponseWriter, r *http.Request) {
	queue := (*p.(*model.Queue))
	cb, cbc := getCallback()
	m := gami.Message{"Action": "QueueRemove", "Queue": queue.Queue, "Interface": queue.Interface}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Message")
	}
}

func queueAdd(p interface{}, w http.ResponseWriter, r *http.Request) {
	queue := (*p.(*model.Queue))
	cb, cbc := getCallback()
	m := gami.Message{
		"Action":         "QueueAdd",
		"Queue":          queue.Queue,
		"Interface":      queue.Interface,
		"StateInterface": queue.StateInterface,
	}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Message")
	}
}

func placeSpy(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.Call))
	o := gami.NewOriginateApp(call.GetChannel(), "ChanSpy", fmt.Sprintf("SIP/%v", call.Exten))
	o.Async = true

	cb, cbc := getCallback()
	if err := ami.GetAMI().Originate(o, nil, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Message")
	}
}

func showChannels(p interface{}, w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	if err := ami.GetAMI().Command("sip show inuse", &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, false, "CmdData")
	}
}

func showInuse(p interface{}, w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	if err := ami.GetAMI().Command("sip show inuse", &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, false, "CmdData")
	}
}

func placeCall(p interface{}, w http.ResponseWriter, r *http.Request) {
	call := (*p.(*model.Call))
	o := gami.NewOriginate(call.GetChannel(), "test", call.Exten, "1")
	o.CallerID = call.GetCallerID()
	o.Async = true

	cb, cbc := getCallback()
	if err := ami.GetAMI().Originate(o, nil, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Message")
	}
}

func shutdown(w http.ResponseWriter, r *http.Request) {
	graceful.Shutdown()
}

func pingAsterisk(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	if err := ami.GetAMI().SendAction(gami.Message{"Action": "Ping"}, &cb); err != nil {
		fmt.Fprint(w, model.Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Ping")
	}
}

func imUp(w http.ResponseWriter, r *http.Request) {
	pretty.Println("Im up, Im up...")
}
