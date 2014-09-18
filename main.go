package main

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kr/pretty"
	"github.com/warik/gami"
	"github.com/zenazn/goji"
	"github.com/zenazn/goji/graceful"

	"github.com/warik/dialer/ami"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/dialer/model"
)

func main() {
	wg := sync.WaitGroup{}

	dh := func(m gami.Message) {
		pretty.Log("Reading message -", m["UniqueID"])
		db.PutChan <- m
	}
	ami.GetAMI().RegisterHandler("Cdr", &dh)

	cdrChan := make(chan gami.Message)
	finishChannels := []chan struct{}{make(chan struct{})}
	ticker := time.NewTicker(conf.CDR_READ_INTERVAL)
	go CdrReader(&wg, cdrChan, finishChannels[len(finishChannels)-1], ticker)

	// Handlers are sending requests to corresponding portals,
	// so their number must be same as number of countries which agency working in
	for i := 0; i < len(conf.GetConf().Agencies); i++ {
		finishChannels = append(finishChannels, make(chan struct{}))
		go CdrHandler(&wg, cdrChan, finishChannels[len(finishChannels)-1], i)
	}

	finishChannels = append(finishChannels, make(chan struct{}))
	go DbHandler(&wg, finishChannels[len(finishChannels)-1])

	initRoutes()
	graceful.PostHook(func() {
		Clean(finishChannels, &wg)
	})
	goji.Serve()
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
	goji.Get("/show_channels", withSignedParams(new(model.DummyStruct), ShowChannels))
	goji.Get("/queue_status", withSignedParams(new(model.DummyStruct), QueueStatus))
	goji.Get("/db_get", withSignedParams(new(model.DbGetter), DBGet))
	goji.Post("/call", withSignedParams(new(model.Call), PlaceCall))
	goji.Post("/spy", withSignedParams(new(model.Call), PlaceSpy))
	goji.Post("/queue_add", withSignedParams(new(model.Queue), QueueAdd))
	goji.Post("/queue_remove", withSignedParams(new(model.Queue), QueueRemove))

	// API for asterisk
	goji.Get("/manager_phone", withStructParams(new(model.PhoneCall), ManagerPhone))
	goji.Get("/manager_phone_for_company",
		withStructParams(new(model.PhoneCall), ManagerPhoneForCompany))
	goji.Get("/show_calling_popup_to_manager",
		withStructParams(new(model.PhoneCall), ShowCallingPopup))
	goji.Get("/show_calling_review_popup_to_manager",
		withStructParams(new(model.PhoneCall), ShowCallingReview))
	goji.Get("/manager_call_after_hours",
		withStructParams(new(model.PhoneCall), ManagerCallAfterHours))

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
