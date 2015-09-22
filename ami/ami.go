package ami

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/model"
	"github.com/warik/go-dialer/util"
)

var (
	QueueState = util.NewSafeMap()
	once       sync.Once
	ami        *gami.Asterisk
)

func GetAMI() *gami.Asterisk {
	once.Do(func() {
		conf := conf.GetConf()
		ami = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
	})
	return ami
}

func connectAndLogin(a *gami.Asterisk) {
	messageAlreadySent := false
	numTries := 1
	for {
		if err := a.Start(); err != nil {
			glog.Errorln(err)
			glog.Warningln("Trying to reconnect and relogin...")
			if !messageAlreadySent {
				conf.Alert("Lost connection with asterisk")
				messageAlreadySent = true
			}
			duration := util.PowInt(conf.AMI_RECONNECT_TIMEOUT, numTries)
			time.Sleep(time.Duration(duration) * time.Second)
			numTries++
			continue
		}
		if messageAlreadySent {
			conf.Alert("Connection with asterisk restored")
			messageAlreadySent = false
		}
		a.SendAction(gami.Message{"Action": "Events", "EventMask": "cdr,call"}, nil)
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

func getInterface(innerNumber string) string {
	return fmt.Sprintf("SIP/%s", innerNumber)
}

func sender(param interface{}) (gami.Message, error) {
	var err error
	cbc := make(chan gami.Message)
	cb := func(m gami.Message) {
		cbc <- m
	}
	glog.Infoln("Sending to asterisk\n", param)
	switch param.(type) {
	case gami.Message:
		err = ami.SendAction(param.(gami.Message), &cb)
	case *gami.Originate:
		err = ami.Originate(param.(*gami.Originate), nil, &cb)
	case string:
		err = ami.Command(param.(string), &cb)
	}
	if err != nil {
		return nil, err
	}
	resp, err := <-cbc, nil
	glog.Infoln("Asterisk response", resp)
	return resp, err
}

func getContext(innerNumber string) (string, error) {
	resp, err := sender(gami.Message{"Action": "SIPShowPeer", "Peer": innerNumber})
	if err != nil {
		return "", err
	}
	if context, ok := resp["Context"]; ok {
		return context, nil
	} else {
		return "", errors.New("Error during SIPShowPeer")
	}
}

func GetStaticQueue(number string) (string, error) {
	resp, err := sender(fmt.Sprintf("database get %s %s", "queues/u2q", number))
	if err != nil {
		return "", err
	}
	if val, ok := resp["Value"]; ok {
		return val, nil
	} else {
		return "", errors.New(resp["CmdData"])
	}
}

func SendMixMonitor(channel, fileName string) (gami.Message, error) {
	m := gami.Message{"Action": "MixMonitor", "Channel": channel, "File": fileName}
	return sender(m)
}

func AddToQueue(queue, innerNumber string) (gami.Message, error) {
	m := gami.Message{
		"Action":    "QueueAdd",
		"Queue":     queue,
		"Interface": getInterface(innerNumber),
	}
	return sender(m)
}

func RemoveFromQueue(queue, innerNumber string) (gami.Message, error) {
	m := gami.Message{
		"Action":    "QueueRemove",
		"Queue":     queue,
		"Interface": getInterface(innerNumber),
	}
	return sender(m)
}

func QueueStatus(queue, innerNumber string) (gami.Message, error) {
	// Before getting new state in queue need to remove old one for case
	// then number was in queue but was removed
	QueueState.Remove(innerNumber)

	m := gami.Message{
		"Action": "QueueStatus",
		"Queue":  queue,
		"Member": getInterface(innerNumber),
	}
	resp, err := sender(m)
	if err != nil {
		return resp, err
	}

	response := gami.Message{"Response": "success"}
	status := QueueState.Get(innerNumber, 1, "-1")

	var responseStatus string
	switch status.(string) {
	case "-1":
		responseStatus = "not_in_queue"
	case "0":
		responseStatus = "not_available"
	case "4":
		responseStatus = "not_available"
	default:
		responseStatus = "available"
	}

	response["StatusKey"] = responseStatus
	return response, nil
}

func GetActiveChannels() (gami.Message, error) {
	return sender("sip show inuse")
}

func Ping() (gami.Message, error) {
	return sender(gami.Message{"Action": "Ping"})
}

func Spy(call model.Call) (gami.Message, error) {
	o := gami.NewOriginateApp(call.GetChannel(), "ChanSpy", fmt.Sprintf("SIP/%v", call.Exten))
	o.Async = true
	return sender(o)
}

func Call(call model.Call) (gami.Message, error) {
	context, err := getContext(call.Inline)
	if err != nil {
		return nil, err
	}
	o := gami.NewOriginate(call.GetChannel(), context,
		strings.TrimPrefix(call.Exten, "+"), "1")
	o.CallerID = call.GetCallerID()
	o.Async = true
	return sender(o)
}

func CallInQueue(call model.CallInQueue) (gami.Message, error) {
	queue := conf.GetConf().GetCallBackQueue(call.Country)
	o := gami.NewOriginate(queue, "manager",
		strings.TrimPrefix(call.PhoneNumber, "+"), "1")
	o.Async = true
	o.CallerID = "<CallMeBack> 777"
	return sender(o)
}
