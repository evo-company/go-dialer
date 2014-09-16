package ami

import (
	"strconv"
	"time"

	"github.com/kr/pretty"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/gami"
)

var ami *gami.Asterisk

var handlersMap = map[string]func(chan gami.Message, <-chan struct{}){
	"Cdr": amiMessageHandler,
}

func connectAndLogin(a *gami.Asterisk) {
	for {
		if err := a.Start(); err != nil {
			pretty.Log("Trying to reconnect and relogin...")
			time.Sleep(conf.AMI_RECONNECT_TIMEOUT)
			continue
		}
		a.SendAction(gami.Message{"Action": "Events", "EventMask": "cdr"}, nil)
		return
	}
}

func startAmi(host, login, password string) (a *gami.Asterisk) {
	a = gami.NewAsterisk(host, login, password)
	netErrHandler := func(err error) {
		connectAndLogin(a)
	}
	a.SetNetErrHandler(&netErrHandler)
	connectAndLogin(a)
	return
}

func amiMessageHandler(mchan chan gami.Message, finishChan <-chan struct{}) {
	i := 1
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing amiMessageHandler")
			return
		case m := <-mchan:
			pretty.Log("Reading message -", m["UniqueID"], strconv.Itoa(i))
			i++
			db.PutChan <- m
		}
	}
}

func GetAMI() *gami.Asterisk {
	return ami
}

func RegisterHandler(event string, finishChan <-chan struct{}) {
	amiMsgChan := make(chan gami.Message)
	dh := func(m gami.Message) {
		amiMsgChan <- m
	}
	GetAMI().RegisterHandler(event, &dh)
	go handlersMap[event](amiMsgChan, finishChan)
}

func init() {
	conf := conf.GetConf()
	ami = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
}
