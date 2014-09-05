package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/boltdb/bolt"
	"github.com/kr/pretty"
	"github.com/parnurzeal/gorequest"
	"github.com/vmihailenco/signer"
	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/model"
	"github.com/warik/gami"
	"github.com/zenazn/goji"
	"github.com/zenazn/goji/graceful"
	"hash"
	"log"
	"net/http"
	_ "net/http/pprof"
	"strconv"
	"strings"
	"time"
)

const (
	REQUEST_TIMEOUT   = 5
	CDR_READ_TIMEOUT  = 30 * time.Second
	REMOTE_ERROR_TEXT = "Error on remote server, status code - %v"
	CDR_DB_FILE       = "cdr_log.db"
	HANDLERS_COUNT    = 1
	BOLT_CDR_BUCKET   = "CdrBucket"
)

type Response map[string]interface{}

func (r Response) String() string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

func clean(finishChannels []chan struct{}) {
	for _, channel := range finishChannels {
		close(channel)
	}
	ami.GetAMI().Logoff()
}

func amiMessageHandler(mchan chan gami.Message, db *bolt.DB, finishChan <-chan struct{}) {
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing amiMessageHandler")
			return
		case m := <-mchan:
			_ = db.Update(func(tx *bolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists([]byte(BOLT_CDR_BUCKET))
				if err != nil {
					pretty.Println(err)
					mchan <- m
				}

				value, _ := json.Marshal(m)
				if err := b.Put([]byte(m["UniqueID"]), value); err != nil {
					pretty.Println(err)
					mchan <- m
				}
				return nil
			})
		}
	}
}

func cdrReader(cdrChan chan<- gami.Message, db *bolt.DB, finishChan <-chan struct{},
	ticker *time.Ticker) {
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing cdrReader")
			ticker.Stop()
			return
		case <-ticker.C:
			_ = db.View(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte(BOLT_CDR_BUCKET))
				if b == nil {
					return nil
				}
				pretty.Log("Reading data from db. Cdr count - ", strconv.Itoa(b.Stats().KeyN))
				c := b.Cursor()
				for k, v := c.First(); k != nil; k, v = c.Next() {
					m := gami.Message{}
					_ = json.Unmarshal(v, &m)
					cdrChan <- m
				}
				return nil
			})
		}
	}
}

func cdrHandler(cdrChan <-chan gami.Message, db *bolt.DB, finishChan <-chan struct{}, i int) {
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing cdrHandler", strconv.Itoa(i))
			return
		case m := <-cdrChan:
			pretty.Log("Processing message -", m["UniqueID"])
			for _, country := range conf.GetConf().Countries {
				url := conf.GetConf().GetApi(country, "save_phone_call")
				if _, err := sendRequest(m, url, "POST"); err == nil {
					_ = db.Update(func(tx *bolt.Tx) error {
						b := tx.Bucket([]byte(BOLT_CDR_BUCKET))
						if err := b.Delete([]byte(m["UniqueID"])); err != nil {
							pretty.Log("Error while deleting message - ", m["UniqueID"])
						}
						return nil
					})
					break
				}
			}
		}
	}
}

func sendRequest(m map[string]string, url string, method string) (string, error) {
	m["CompanyId"] = conf.GetConf().CompanyId
	signedData, err := signData(m)
	if err != nil {
		return "", err
	}

	request := gorequest.New()
	if method == "POST" {
		request.Post(url).Send(model.SignedData{Data: signedData})
	} else if method == "GET" {
		query, _ := json.Marshal(model.SignedData{Data: signedData})
		request.Get(url).Query(string(query))
	}

	resp, respBody, errs := request.Timeout(REQUEST_TIMEOUT * time.Second).End()

	if len(errs) != 0 {
		return "", errs[0]
	}

	if resp.StatusCode != 200 {
		return "", errors.New(fmt.Sprintf(REMOTE_ERROR_TEXT, resp.StatusCode))
	}

	return respBody, nil
}

func getCallback() (cb func(gami.Message), cbc chan gami.Message) {
	cbc = make(chan gami.Message)
	cb = func(m gami.Message) {
		pretty.Log("Handling response...\n", m)
		cbc <- m
	}
	return
}

func signData(m map[string]string) (signedData string, err error) {
	h := hmac.New(func() hash.Hash {
		return sha1.New()
	}, []byte(conf.GetConf().Secret))

	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	signedData = string(signer.NewBase64Signer(h).Sign(data))
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
	fmt.Fprint(w, Response{"status": status, "response": resp[dataKey]})
}

func main() {
	db, err := bolt.Open(CDR_DB_FILE, 0600, nil)
	if err != nil {
		log.Fatalln(err)
	}
	defer db.Close()

	finishChannels := []chan struct{}{make(chan struct{})}

	amiMsgChan := make(chan gami.Message)
	dh := func(m gami.Message) {
		amiMsgChan <- m
	}
	ami.GetAMI().RegisterHandler("Cdr", &dh)
	go amiMessageHandler(amiMsgChan, db, finishChannels[len(finishChannels)-1])

	cdrChan := make(chan gami.Message)
	finishChannels = append(finishChannels, make(chan struct{}))
	ticker := time.NewTicker(CDR_READ_TIMEOUT)
	go cdrReader(cdrChan, db, finishChannels[len(finishChannels)-1], ticker)

	for i := 0; i < HANDLERS_COUNT; i++ {
		finishChannels = append(finishChannels, make(chan struct{}))
		go cdrHandler(cdrChan, db, finishChannels[len(finishChannels)-1], i)
	}

	initRoutes()
	graceful.PostHook(func() {
		clean(finishChannels)
	})
	goji.Serve()
}

func initRoutes() {
	goji.Get("/", imUp)
	goji.Get("/ping-asterisk", pingAsterisk)
	goji.Post("/shutdown", shutdown)

	//API for prom
	goji.Get("/show_inuse", showInuse)
	goji.Get("/show_channels", showChannels)
	goji.Get("/queue_status", queueStatus)
	goji.Get("/db_get", withStructParams(new(model.DbGetter), dbGet))
	goji.Post("/call", withStructParams(new(model.Call), placeCall))
	goji.Post("/spy", withStructParams(new(model.Call), placeSpy))
	goji.Post("/queue_add", withStructParams(new(model.Queue), queueAdd))
	goji.Post("/queue_remove", withStructParams(new(model.Queue), queueRemove))

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
}

func JSONReponse(h http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "json")
		h.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func withStructParams(i interface{}, h func(interface{}, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := model.GetStructFromParams(r, i); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h(i, w, r)
	}
}

func managerCallAfterHours(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_call_after_hours")
	resp, err := sendRequest(payload, url, "POST")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, Response{"status": resp})
	}
}

func showCallingReview(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{
		"inner_number": phoneCall.InnerNumber,
		"review_href":  phoneCall.ReviewHref,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_review_popup_to_manager")
	resp, err := sendRequest(payload, url, "POST")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, Response{"status": resp})
	}
}

func showCallingPopup(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{
		"inner_number":  phoneCall.InnerNumber,
		"calling_phone": phoneCall.CallingPhone,
	}
	url := conf.GetConf().GetApi(phoneCall.Country, "show_calling_popup_to_manager")
	resp, err := sendRequest(payload, url, "POST")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, Response{"status": resp})
	}
}

func managerPhoneForCompany(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"id": phoneCall.Id}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone_for_company")
	resp, err := sendRequest(payload, url, "GET")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, Response{"inner_number": resp})
	}
}

func managerPhone(p interface{}, w http.ResponseWriter, r *http.Request) {
	phoneCall := (*p.(*model.PhoneCall))
	payload := map[string]string{"calling_phone": phoneCall.CallingPhone}
	url := conf.GetConf().GetApi(phoneCall.Country, "manager_phone")
	resp, err := sendRequest(payload, url, "GET")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	} else {
		fmt.Fprint(w, Response{"inner_number": resp})
	}
}

func dbGet(p interface{}, w http.ResponseWriter, r *http.Request) {
	dbGetter := (*p.(*model.DbGetter))
	cb, cbc := getCallback()
	command := fmt.Sprintf("database get %s %s", dbGetter.Family, dbGetter.Key)
	if err := ami.GetAMI().Command(command, &cb); err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, false, "CmdData")
	}
}

func queueStatus(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	m := gami.Message{"Action": "QueueStatus"}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Message")
	}
}

func queueRemove(p interface{}, w http.ResponseWriter, r *http.Request) {
	queue := (*p.(*model.Queue))
	cb, cbc := getCallback()
	m := gami.Message{"Action": "QueueRemove", "Queue": queue.Queue, "Interface": queue.Interface}
	if err := ami.GetAMI().SendAction(m, &cb); err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
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
		fmt.Fprint(w, Response{"status": "error", "error": err})
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
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Message")
	}
}

func showChannels(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	if err := ami.GetAMI().Command("sip show inuse", &cb); err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, false, "CmdData")
	}
}

func showInuse(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	if err := ami.GetAMI().Command("sip show inuse", &cb); err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
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
		fmt.Fprint(w, Response{"status": "error", "error": err})
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
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		writeResponse(w, <-cbc, true, "Ping")
	}
}

func imUp(w http.ResponseWriter, r *http.Request) {
	pretty.Println("Im up, Im up...")
}
