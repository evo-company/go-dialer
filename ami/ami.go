package ami

import (
	"code.google.com/p/gami"
	"github.com/warik/dialer/conf"
	"log"
	"net"
)

var ami *gami.Asterisk

func startAmi(host, login, password string) (g *gami.Asterisk) {
	c, err := net.Dial("tcp", host)
	if err != nil {
		log.Fatalln(err)
	}
	g = gami.NewAsterisk(&c, nil)
	if err = g.Login(login, password); err != nil {
		log.Fatalln(err)
	}
	return
}

func GetAMI() *gami.Asterisk {
	return ami
}

func init() {
	conf := conf.GetConf()
	ami = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
}
