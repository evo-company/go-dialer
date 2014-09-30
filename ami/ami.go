package ami

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/warik/gami"

	"github.com/warik/dialer/conf"
)

var ami *gami.Asterisk

func connectAndLogin(a *gami.Asterisk) {
	wasError := false
	messageAlreadySent := false
	for {
		if err := a.Start(); err != nil {
			wasError = true
			glog.Warningln("Trying to reconnect and relogin...")
			if !messageAlreadySent {
				conf.Alert("Lost connection with asterisk")
				messageAlreadySent = true
			}
			time.Sleep(conf.AMI_RECONNECT_TIMEOUT)
			continue
		}
		if wasError {
			conf.Alert("Connection with asterisk restored")
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

func getInterface(country, innerNumber string) (string, string) {
	state := fmt.Sprintf("SIP/%s", innerNumber)
	ifc := fmt.Sprintf("Local/%s%s@Queue_Members/n", innerNumber, country)
	return ifc, state
}

func GetCallback() (cb func(gami.Message), cbc chan gami.Message) {
	cbc = make(chan gami.Message)
	cb = func(m gami.Message) {
		cbc <- m
	}
	return
}

func GetStaticQueue(number string) (string, error) {
	cb, cbc := GetCallback()
	command := fmt.Sprintf("database get %s %s", "queues/u2q", number)
	if err := ami.Command(command, &cb); err != nil {
		glog.Errorln(err)
		return "", err
	}
	resp := <-cbc
	if val, ok := resp["Value"]; ok {
		return val, nil
	} else {
		return "", errors.New(resp["CmdData"])
	}
}

func AddToQueue(queue, country, innerNumber string) (gami.Message, error) {
	cb, cbc := GetCallback()
	ifc, state := getInterface(country, innerNumber)
	m := gami.Message{
		"Action":         "QueueAdd",
		"Queue":          queue,
		"Interface":      ifc,
		"StateInterface": state,
	}
	if err := ami.SendAction(m, &cb); err != nil {
		return nil, err
	} else {
		return <-cbc, nil
	}
}

func RemoveFromQueue(queue, country, innerNumber string) (gami.Message, error) {
	cb, cbc := GetCallback()
	ifc, _ := getInterface(country, innerNumber)
	m := gami.Message{"Action": "QueueRemove", "Queue": queue, "Interface": ifc}
	if err := ami.SendAction(m, &cb); err != nil {
		return nil, err
	} else {
		return <-cbc, nil
	}
}

func QueueStatus() error {
	return ami.SendAction(gami.Message{"Action": "QueueStatus"}, nil)
}

func GetAMI() *gami.Asterisk {
	return ami
}

func init() {
	conf := conf.GetConf()
	ami = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
}
