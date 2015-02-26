package ami

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/go-dialer/conf"
	"github.com/warik/go-dialer/model"
)

var ami *gami.Asterisk

func GetAMI() *gami.Asterisk {
	return ami
}

func connectAndLogin(a *gami.Asterisk) {
	messageAlreadySent := false
	for {
		if err := a.Start(); err != nil {
			glog.Errorln(err)
			glog.Warningln("Trying to reconnect and relogin...")
			if !messageAlreadySent {
				conf.Alert("Lost connection with asterisk")
				messageAlreadySent = true
			}
			time.Sleep(conf.AMI_RECONNECT_TIMEOUT)
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

func getInterface(country, innerNumber string) (string, string) {
	state := fmt.Sprintf("SIP/%s", innerNumber)
	ifc := fmt.Sprintf("Local/%s%s@Queue_Members/n", innerNumber, country)
	return ifc, state
}

func sender(param interface{}) (gami.Message, error) {
	var err error
	cbc := make(chan gami.Message)
	cb := func(m gami.Message) {
		cbc <- m
	}
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
	return <-cbc, nil
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

func AddToQueue(queue, country, innerNumber string) (gami.Message, error) {
	ifc, state := getInterface(country, innerNumber)
	m := gami.Message{
		"Action":         "QueueAdd",
		"Queue":          queue,
		"Interface":      ifc,
		"StateInterface": state,
	}
	return sender(m)
}

func RemoveFromQueue(queue, country, innerNumber string) (gami.Message, error) {
	ifc, _ := getInterface(country, innerNumber)
	m := gami.Message{"Action": "QueueRemove", "Queue": queue, "Interface": ifc}
	return sender(m)
}

func QueueStatus() error {
	return ami.SendAction(gami.Message{"Action": "QueueStatus"}, nil)
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
	o := gami.NewOriginate(call.GetChannel(), context, strings.TrimPrefix(call.Exten, "+"), "1")
	o.CallerID = call.GetCallerID()
	o.Async = true
	return sender(o)
}

func init() {
	conf := conf.GetConf()
	ami = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
}
