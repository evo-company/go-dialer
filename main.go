package main

import (
	_ "net/http/pprof"

	"flag"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/warik/gami"
	"github.com/zenazn/goji"

	"github.com/warik/go-dialer/ami"
	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/db"
	"github.com/warik/go-dialer/model"
	"github.com/warik/go-dialer/util"
)

var (
	savePhoneCalls = flag.Bool("save_calls", false, "Set true to save phone calls")
	sendCalls      = flag.Bool("send_calls", false, "Set true to convert and send phone calls")
	manageQueues   = flag.Bool("manage_queues", false, "Set true to enable asterisk queue management")
)

func init() {
	flag.Parse()
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func main() {
	// CdrReader reads cdrs from db once in a while and sends them to CdrSender
	wg := sync.WaitGroup{}
	finishChannels := []chan struct{}{make(chan struct{})}

	numbersChan := make(chan []string, 10)
	wg.Add(1)
	go NumbersLoader(&wg, numbersChan, finishChannels[len(finishChannels)-1],
		time.NewTicker(conf.NUMBERS_LOAD_INTERVAL))
	util.LoadInnerNumbers(numbersChan)

	finishChannels = append(finishChannels, make(chan struct{}))
	wg.Add(1)
	mChan := make(chan db.CDR, conf.MAX_CDR_NUMBER*2)
	go CdrReader(&wg, mChan, finishChannels[len(finishChannels)-1],
		time.NewTicker(conf.CDR_READ_INTERVAL))

	// CdrSender tries to send cdr to related portal
	// in case of success - deletes it from db
	for i := 0; i < conf.CDR_SAVERS_COUNT; i++ {
		finishChannels = append(finishChannels, make(chan struct{}))
		wg.Add(1)
		go CdrSender(&wg, mChan, finishChannels[len(finishChannels)-1], i+1)
	}

	if *sendCalls {
		// PhoneCallReader gets unique phone calls ids from db and sends them to PhoneCallSender
		finishChannels = append(finishChannels, make(chan struct{}))
		wg.Add(1)
		pcChan := make(chan db.PhoneCall, conf.MAX_CDR_NUMBER*2)
		go PhoneCallReader(&wg, pcChan, finishChannels[len(finishChannels)-1],
			time.NewTicker(conf.PHONE_CALLS_SAVE_INTERVAL))

		// PhoneCallSender gets phone call wav audio file by uniqueId, converts it to mp3 and sends to
		// storage
		for i := 0; i < conf.PHONE_CALL_SENDERS_COUNT; i++ {
			finishChannels = append(finishChannels, make(chan struct{}))
			wg.Add(1)
			go PhoneCallSender(&wg, pcChan, finishChannels[len(finishChannels)-1], i+1)
		}
	}

	if *manageQueues {
		queueTransport := make(chan chan gami.Message)
		// QueueMember handler gathers information about current queue occupations
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
		qsch := func(m gami.Message) {
			queueTransport <- queueStatusChan
		}
		ami.GetAMI().RegisterHandler("QueueStatusComplete", &qsch)

		// QueueManager does some heavy stuff on managing asterisk queues according to managers
		// status and sends fresh data to portal, so the portal works only with local data
		finishChannels = append(finishChannels, make(chan struct{}))
		wg.Add(1)
		go QueueManager(&wg, queueTransport, finishChannels[len(finishChannels)-1],
			time.NewTicker(conf.QUEUE_RENEW_INTERVAL))
	}

	if *savePhoneCalls {
		// PhoneCallRecordStarter initiates MixMonitor for call recording
		pch := PhoneCallRecordStarter
		ami.GetAMI().RegisterHandler("BridgeEnter", &pch)
	}

	// CdrEventHandler reads cdrs, processes them and stores in db for further sending to
	// corresponding portals
	ceh := CdrEventHandler
	ami.GetAMI().RegisterHandler("Cdr", &ceh)

	initRoutes()
	goji.Serve()

	// We need to switch ami first of all to avoid it sending any data
	ami.GetAMI().Logoff()
	// Send closing events to all goroutines
	for _, channel := range finishChannels {
		close(channel)
	}

	wg.Wait()
	glog.Flush()
}

func initRoutes() {
	// API for self
	goji.Get("/", ImUp)
	goji.Get("/ping", PingAsterisk)
	goji.Get("/check-portals", CheckPortals)
	goji.Get("/stats", Stats)
	goji.Get("/cdr/count", CdrCount)
	goji.Get("/cdr/get", withStructParams(new(model.Cdr), GetCdr))
	goji.Post("/cdr/delete", withStructParams(new(model.Cdr), DeleteCdr))

	//API for prom
	goji.Get("/show_inuse", withSignedParams(new(model.DummyStruct), ShowInuse))
	goji.Post("/call", withSignedParams(new(model.Call), PlaceCall))
	goji.Post("/call_in_queue", withStructParams(new(model.CallInQueue), PlaceCallInQueue))
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
			glog.Errorln(err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := util.UnsignData(i, (*signedData)); err != nil {
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
			glog.Errorln(err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		glog.Infoln("<<< INPUT PARAMS", i)
		h(i, w, r)
	}
}
