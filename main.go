package main

import (
	"code.google.com/p/gami"
	"encoding/json"
	"fmt"
	"github.com/kr/pretty"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/model"
	"github.com/zenazn/goji"
	"github.com/zenazn/goji/graceful"
	"github.com/zenazn/goji/web"
	"log"
	"net"
	"net/http"
	"strings"
)

var AMI *gami.Asterisk

type Response map[string]interface{}

func (r Response) String() string {
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	return string(b)
}

func startAmi(host, login, password string) (*gami.Asterisk, error) {
	c, err := net.Dial("tcp", host)
	if err != nil {
		return nil, err
	}
	g := gami.NewAsterisk(&c, nil)
	err = g.Login(login, password)
	if err != nil {
		return nil, err
	}
	return g, nil
}

func cleanAmi() {
	AMI.Logoff()
}

func amiMessageHandler(mchan chan gami.Message) {
	for {
		pretty.Log(<-mchan)
	}
}

func getCallback() (cb func(gami.Message), cbc chan gami.Message) {
	cbc = make(chan gami.Message)
	cb = func(m gami.Message) {
		pretty.Log(m)
		cbc <- m
	}
	return
}

func init() {
	conf, err := conf.GetConf("conf.json")
	if err != nil {
		log.Fatalln(err)
	}

	AMI, err = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
	if err != nil {
		log.Fatalln(err)
	}
	AMIMessageChan := make(chan gami.Message)
	dh := func(m gami.Message) {
		AMIMessageChan <- m
	}
	AMI.RegisterHandler("Cdr", &dh)
	go amiMessageHandler(AMIMessageChan)
}

func main() {
	initRoutes()
	graceful.PostHook(cleanAmi)
	goji.Serve()
}

func initRoutes() {
	goji.Get("/", imUp)
	goji.Get("/ping-asterisk", pingAsterisk)
	goji.Post("/call", placeCall)
	goji.Get("/show_inuse", showInuse)
	goji.Get("/show_channels", showChannels)
	goji.Post("/spy", placeSpy)
	goji.Post("/queue_add", queueAdd)
	goji.Post("/queue_remove", queueRemove)
	goji.Get("/queue_status", queueStatus)
	goji.Use(JSONReponse)
}

func JSONReponse(h http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "json")
		h.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func queueStatus(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	err := AMI.SendAction(gami.Message{"Action": "QueueStatus"}, &cb)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(
			w,
			Response{"status": strings.ToLower(resp["Response"]), "response": resp["Message"]},
		)
	}
}

func queueRemove(w http.ResponseWriter, r *http.Request) {
	queue := new(model.Queue)
	err := model.GetStructFromParams(r, queue)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cb, cbc := getCallback()
	err = AMI.SendAction(
		gami.Message{"Action": "QueueRemove", "Queue": queue.Queue, "Interface": queue.Interface},
		&cb,
	)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(
			w,
			Response{"status": strings.ToLower(resp["Response"]), "response": resp["Message"]},
		)
	}
}

func queueAdd(c web.C, w http.ResponseWriter, r *http.Request) {
	queue := new(model.Queue)
	err := model.GetStructFromParams(r, queue)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cb, cbc := getCallback()
	err = AMI.SendAction(
		gami.Message{
			"Action":         "QueueAdd",
			"Queue":          queue.Queue,
			"Interface":      queue.Interface,
			"StateInterface": queue.StateInterface,
		},
		&cb,
	)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(
			w,
			Response{"status": strings.ToLower(resp["Response"]), "response": resp["Message"]},
		)
	}
}

func placeSpy(c web.C, w http.ResponseWriter, r *http.Request) {
	call := new(model.Call)
	err := model.GetStructFromParams(r, call)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	o := gami.NewOriginateApp(call.GetChannel(), "ChanSpy", fmt.Sprintf("SIP/%v", call.Exten))
	o.Async = true

	cb, cbc := getCallback()
	err = AMI.Originate(o, nil, &cb)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(
			w,
			Response{"status": strings.ToLower(resp["Response"]), "response": resp["Message"]},
		)
	}
}

func showChannels(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	err := AMI.Command("sip show inuse", &cb)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(w, Response{"status": "success", "response": resp["CmdData"]})
	}
}

func showInuse(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	err := AMI.Command("sip show inuse", &cb)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(w, Response{"status": "success", "response": resp["CmdData"]})
	}
}

func placeCall(c web.C, w http.ResponseWriter, r *http.Request) {
	call := new(model.Call)
	err := model.GetStructFromParams(r, call)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pretty.Printf("Trying to place a call. Inline: %v, Exten: %v\n", call.Inline, call.Exten)

	o := gami.NewOriginate(call.GetChannel(), "test", call.Exten, "1")
	o.CallerID = call.GetCallerID()
	o.Async = true

	cb, cbc := getCallback()
	err = AMI.Originate(o, nil, &cb)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(
			w,
			Response{"status": strings.ToLower(resp["Response"]), "response": resp["Message"]},
		)
	}
}

func pingAsterisk(w http.ResponseWriter, r *http.Request) {
	cb, cbc := getCallback()
	err := AMI.SendAction(gami.Message{"Action": "Ping"}, &cb)
	if err != nil {
		fmt.Fprint(w, Response{"status": "error", "error": err})
	} else {
		resp := <-cbc
		fmt.Fprint(
			w,
			Response{"status": strings.ToLower(resp["Response"]), "response": resp["Ping"]},
		)
	}
}

func imUp(w http.ResponseWriter, r *http.Request) {
	pretty.Println("Im up, Im up...")
}
