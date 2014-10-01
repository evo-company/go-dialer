package main

import (
	"flag"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/warik/gami"
	"github.com/zenazn/goji"
	"github.com/zenazn/goji/graceful"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/model"
)

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func main() {
	flag.Parse()

	// Lets get inner numbers first of all
	getInnerNumbers()

	wg := sync.WaitGroup{}

	// CdrEventHandler reads cdrs, processes them and stores in db for further sending to
	// corresponding portals
	ceh := CdrEventHandler
	ami.GetAMI().RegisterHandler("Cdr", &ceh)

	// We need QueueMember handler for gather information about current queue occupations
	// those events start to come after QueueStatus action will be sent
	queueStatusChan := make(chan gami.Message, 1000)
	qmh := func(m gami.Message) {
		if strings.HasPrefix(m["Name"], "Local") {
			queueStatusChan <- m
		}
	}
	ami.GetAMI().RegisterHandler("QueueMember", &qmh)

	// QueueStatusComplete tells us that queueMember events were sent, so we can send this
	// info to queueManager and use for determination of managers states in queues
	queueTransport := make(chan chan gami.Message)
	qsch := func(m gami.Message) {
		queueTransport <- queueStatusChan
	}
	ami.GetAMI().RegisterHandler("QueueStatusComplete", &qsch)

	cdrChan := make(chan gami.Message, conf.MAX_CDR_NUMBER)
	finishChannels := []chan struct{}{make(chan struct{})}
	go CdrReader(&wg, cdrChan, finishChannels[len(finishChannels)-1],
		time.NewTicker(conf.CDR_READ_INTERVAL))

	for i := 0; i < conf.HANDLERS_NUMBER; i++ {
		finishChannels = append(finishChannels, make(chan struct{}))
		go CdrSaver(&wg, cdrChan, finishChannels[len(finishChannels)-1], i)
	}

	finishChannels = append(finishChannels, make(chan struct{}))
	go DbHandler(&wg, finishChannels[len(finishChannels)-1])

	if conf.GetConf().Name == "prom" {
		finishChannels = append(finishChannels, make(chan struct{}))
		go QueueManager(&wg, queueTransport, finishChannels[len(finishChannels)-1],
			time.NewTicker(conf.QUEUE_RENEW_INTERVAL))
	}

	initRoutes()
	graceful.PostHook(func() {
		Clean(finishChannels, &wg)
	})
	goji.Serve()
	wg.Wait()
	// panic("Manual panic for checking goroutines")
}

func initRoutes() {
	// API for self
	goji.Get("/", ImUp)
	goji.Get("/ping", PingAsterisk)
	goji.Get("/db-stats", DBStats)
	goji.Get("/cdr/number", CdrNumber)
	goji.Get("/cdr/get", withStructParams(new(model.Cdr), GetCdr))
	goji.Post("/cdr/delete", withStructParams(new(model.Cdr), DeleteCdr))

	//API for prom
	goji.Get("/show_inuse", withSignedParams(new(model.DummyStruct), ShowInuse))
	// goji.Get("/show_channels", withSignedParams(new(model.DummyStruct), ShowChannels))
	goji.Post("/call", withSignedParams(new(model.Call), PlaceCall))
	goji.Post("/spy", withSignedParams(new(model.Call), PlaceSpy))
	goji.Post("/queue_add", withSignedParams(new(model.PhoneCall), QueueAdd))
	goji.Post("/queue_remove", withSignedParams(new(model.PhoneCall), QueueRemove))

	// API for asterisk
	goji.Get("/api/manager_phone", withStructParams(new(model.PhoneCall), ManagerPhone))
	goji.Get("/api/manager_phone_for_company",
		withStructParams(new(model.PhoneCall), ManagerPhoneForCompany))
	goji.Get("/api/show_calling_popup_to_manager",
		withStructParams(new(model.PhoneCall), ShowCallingPopup))
	goji.Get("/api/show_calling_review_popup_to_manager",
		withStructParams(new(model.PhoneCall), ShowCallingReview))
	goji.Get("/api/manager_call_after_hours",
		withStructParams(new(model.PhoneCall), ManagerCallAfterHours))

	goji.Use(JSONReponse)
	// goji.Use(AllowedRemoteAddress)
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
		glog.Warningln("Request from not allowed IP", r.RemoteAddr)
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
			glog.Errorln(err.Error())
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		glog.Infoln("<<< INPUT PARAMS", i)
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
		glog.Infoln("<<< INPUT PARAMS", i)
		h(i, w, r)
	}
}
