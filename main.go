package main

import (
	_ "crypto/sha512"
	_ "net/http/pprof"

	"flag"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/context"

	"github.com/golang/glog"
	"github.com/zenazn/goji"

	"github.com/warik/gami"
	"github.com/warik/go-dialer/ami"
	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/db"
	"github.com/warik/go-dialer/model"
	"github.com/warik/go-dialer/util"
)

var (
	signedInput    = flag.Bool("signed_input", true, "Set true to check input params for correct signature")
	savePhoneCalls = flag.Bool("save_calls", false, "Set true to save phone calls")
	showPopups     = flag.Bool("show_popups", false, "Set true to show popups on portal before and after call")
	sendCalls      = flag.Bool("send_calls", false, "Set true to convert and send phone calls")
	manageQueues   = flag.Bool("manage_queues", false, "Set true to enable asterisk queue management")
)

func init() {
	flag.Parse()
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func main() {
	wg := sync.WaitGroup{}
	ctx, cancelFunc := context.WithCancel(context.Background())

	numbersChan := make(chan []string, 10)
	NumbersLoader(ctx, &wg, numbersChan, time.NewTicker(conf.NUMBERS_LOAD_INTERVAL))
	util.LoadInnerNumbers(numbersChan)

	mChan := make(chan db.CDR, conf.MAX_CDR_NUMBER*2)
	// CdrReader reads cdrs from db once in a while and sends them to CdrSender
	CdrReader(ctx, &wg, mChan, time.NewTicker(conf.CDR_READ_INTERVAL))

	// CdrSender tries to send cdr to related portal
	// in case of success - deletes it from db
	for i := 0; i < conf.CDR_SAVERS_COUNT; i++ {
		CdrSender(ctx, &wg, mChan, i+1)
	}

	if *sendCalls {
		// PhoneCallReader gets unique phone calls ids from db and sends them to PhoneCallSender
		pcChan := make(chan db.PhoneCall, conf.MAX_CDR_NUMBER*2)
		PhoneCallReader(ctx, &wg, pcChan, time.NewTicker(conf.PHONE_CALLS_SAVE_INTERVAL))

		// PhoneCallSender gets phone call wav audio file by uniqueId, converts it to mp3 and sends to
		// storage
		for i := 0; i < conf.PHONE_CALL_SENDERS_COUNT; i++ {
			PhoneCallSender(ctx, &wg, pcChan, i+1)
		}
	}

	// Old way to handle queues
	// if *manageQueues {
	// queueTransport := make(chan chan gami.Message)
	// // QueueMember handler gathers information about current queue occupations
	// // those events start to come after QueueStatus action will be sent
	// queueStatusChan := make(chan gami.Message, 1000)
	// qmh := func(m gami.Message) {
	// 	if strings.HasPrefix(m["Name"], "Local") {
	// 		queueStatusChan <- m
	// 	}
	// }
	// ami.GetAMI().RegisterHandler("QueueMember", &qmh)

	// // QueueStatusComplete tells us that queueMember events were sent, so we can send this
	// // info to queueManager and use for determination of managers states in queues
	// qsch := func(m gami.Message) {
	// 	queueTransport <- queueStatusChan
	// }
	// ami.GetAMI().RegisterHandler("QueueStatusComplete", &qsch)
	//
	// // QueueManager does some heavy stuff on managing asterisk queues according to managers
	// // status and sends fresh data to portal, so the portal works only with local data
	// finishChannels = append(finishChannels, make(chan struct{}))
	// wg.Add(1)
	// go QueueManager(&wg, queueTransport, finishChannels[len(finishChannels)-1],
	// 	time.NewTicker(conf.QUEUE_RENEW_INTERVAL))
	// }

	if *savePhoneCalls || *showPopups {
		// BridgeEventHandler initiates MixMonitor for call recording
		// and shows popup for manager in portal
		beh := BridgeEventHandler
		ami.GetAMI().RegisterHandler("BridgeEnter", &beh)
	}

	// Handles response from queue status action
	qmh := func(m gami.Message) {
		ami.QueueState.Put(strings.Split(m["Name"], "/")[1], m["Status"])
	}
	ami.GetAMI().RegisterHandler("QueueMember", &qmh)

	// CdrEventHandler reads cdrs, processes them and stores in db for further
	// sending to corresponding portals
	ceh := CdrEventHandler
	ami.GetAMI().RegisterHandler("Cdr", &ceh)

	initRoutes()
	goji.Serve()

	// We need to switch ami first of all to avoid it sending any data
	ami.GetAMI().Logoff()
	// Send closing events to all goroutines
	cancelFunc()

	wg.Wait()
	glog.Flush()
}

func initRoutes() {
	// API for self
	goji.Get("/", ImUp)
	goji.Get("/ping", AmiHandler{nil, PingAsterisk})
	goji.Get("/check-portals", CheckPortals)
	goji.Get("/stats", Stats)
	goji.Get("/cdr/count", CdrCount)
	goji.Get("/cdr/get", ApiHandler{new(model.Cdr), GetCdr})
	goji.Post("/cdr/delete", ApiHandler{new(model.Cdr), DeleteCdr})

	//API for prom
	goji.Get("/show_inuse", AmiHandler{new(model.DummyStruct), ShowInuse})
	goji.Post("/call", AmiHandler{new(model.Call), PlaceCall})
	goji.Post("/call_in_queue", AmiHandler{new(model.CallInQueue), PlaceCallInQueue})
	goji.Post("/spy", AmiHandler{new(model.Call), PlaceSpy})
	goji.Post("/queue_add", AmiHandler{new(model.QueueContainer), QueueAdd})
	goji.Post("/queue_remove", AmiHandler{new(model.QueueContainer), QueueRemove})
	goji.Get("/queue_status", AmiHandler{new(model.QueueContainer), QueueStatus})

	// API for asterisk
	goji.Get("/api/manager_phone", ApiHandler{new(model.PhoneCall), ManagerPhone})
	goji.Get("/api/manager_phone_for_company",
		ApiHandler{new(model.PhoneCall), ManagerPhoneForCompany})
	goji.Get("/api/show_calling_popup_to_manager",
		ApiHandler{new(model.PhoneCall), ShowCallingPopup})
	goji.Get("/api/show_calling_review_popup_to_manager",
		ApiHandler{new(model.PhoneCall), ShowCallingReview})
	goji.Get("/api/manager_call_after_hours",
		ApiHandler{new(model.PhoneCall), ManagerCallAfterHours})

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
