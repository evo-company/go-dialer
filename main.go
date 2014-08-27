package main

import (
	"code.google.com/p/gami"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kr/pretty"
	"github.com/parnurzeal/gorequest"
	"github.com/vmihailenco/signer"
	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/model"
	"github.com/zenazn/goji"
	"github.com/zenazn/goji/graceful"
	"hash"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	REQUEST_TIMEOUT   = 5
	REMOTE_ERROR_TEXT = "Error on remote server"
	AMI_LOG_FILE      = "ami_log.csv"
)

type Response map[string]interface{}

func (r Response) String() string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

func cleanAmi() {
	ami.GetAMI().Logoff()
}

func amiMessageHandler(mchan chan gami.Message) {
	for {
		m := <-mchan
		pretty.Log("Handling message from AMI...\n", m)
		if m["Event"] != "Cdr" {
			return
		}
		for _, domain := range conf.GetConf().Portals {
			go logAmiMessage(m, domain+conf.GetConf().Api+"save_phone_call")
		}
	}
}

func logAmiMessage(m gami.Message, url string) {
	if _, err := sendRequest(m, url, "POST"); err != nil {
		var file *os.File
		if _, err := os.Stat(AMI_LOG_FILE); os.IsNotExist(err) {
			file, err = os.Create(AMI_LOG_FILE)
			if err != nil {
				pretty.Log(err)
				return
			}
		} else {
			file, err = os.OpenFile(AMI_LOG_FILE, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				pretty.Log(err)
				return
			}
		}
		defer file.Close()

		w := csv.NewWriter(file)
		w.Write([]string{"", m["Source"], m["Destination"], m["DestinationContext"], m["CallerID"],
			m["Channel"], m["DestinationChannel"], m["LastApplication"], m["LastData"],
			m["StartTime"], m["AnswerTime"], m["EndTime"], m["Duration"], m["BillableSeconds"],
			m["Disposition"], m["AMAFlags"], m["UniqueID"], ""})
		w.Flush()
	}
}

func sendRequest(m map[string]string, url string, method string) (string, error) {
	m["AgencyId"] = conf.GetConf().AgencyId
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
		for _, err := range errs {
			pretty.Log(err.Error())
		}
		return "", errs[0]
	}

	if resp.StatusCode != 200 {
		return "", errors.New(REMOTE_ERROR_TEXT)
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

func init() {
	amiMsgChan := make(chan gami.Message)
	dh := func(m gami.Message) {
		amiMsgChan <- m
	}
	ami.GetAMI().RegisterHandler("Cdr", &dh)
	go amiMessageHandler(amiMsgChan)
}

func main() {
	initRoutes()
	graceful.PostHook(cleanAmi)
	goji.Serve()
}

func initRoutes() {
	goji.Get("/", imUp)
	goji.Get("/ping-asterisk", pingAsterisk)

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
	url := conf.GetConf().Portals[phoneCall.Country] +
		conf.GetConf().Api + "manager_call_after_hours"

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
	url := conf.GetConf().Portals[phoneCall.Country] +
		conf.GetConf().Api + "show_calling_review_popup_to_manager"

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
	url := conf.GetConf().Portals[phoneCall.Country] +
		conf.GetConf().Api + "show_calling_popup_to_manager"

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
	url := conf.GetConf().Portals[phoneCall.Country] + conf.GetConf().Api + "manager_phone_for_company"

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
	url := conf.GetConf().Portals[phoneCall.Country] + conf.GetConf().Api + "manager_phone"

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
