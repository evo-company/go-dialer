package ami

import (
	"time"

	"github.com/kr/pretty"

	"github.com/warik/dialer/conf"
	"github.com/warik/gami"
)

var ami *gami.Asterisk

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

func GetAMI() *gami.Asterisk {
	return ami
}

func init() {
	conf := conf.GetConf()
	ami = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
}
