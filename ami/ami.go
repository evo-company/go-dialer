package ami

import (
	"encoding/json"
	"log"
	"net"

	"github.com/boltdb/bolt"
	"github.com/kr/pretty"
	"github.com/warik/dialer/conf"
	"github.com/warik/dialer/db"
	"github.com/warik/gami"
)

var ami *gami.Asterisk

var handlersMap = map[string]func(chan gami.Message, <-chan struct{}){
	"Cdr": amiMessageHandler,
}

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

func amiMessageHandler(mchan chan gami.Message, finishChan <-chan struct{}) {
	for {
		select {
		case <-finishChan:
			pretty.Log("Finishing amiMessageHandler")
			return
		case m := <-mchan:
			_ = db.GetDB().Update(func(tx *bolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists([]byte(conf.BOLT_CDR_BUCKET))
				if err != nil {
					pretty.Println(err)
					mchan <- m
				}

				value, _ := json.Marshal(m)
				if err := b.Put([]byte(m["UniqueID"]), value); err != nil {
					pretty.Println(err)
					mchan <- m
				}
				return nil
			})
		}
	}
}

func GetAMI() *gami.Asterisk {
	return ami
}

func RegisterHandler(event string, finishChan <-chan struct{}) {
	amiMsgChan := make(chan gami.Message)
	dh := func(m gami.Message) {
		pretty.Log("Reading message -", m["UniqueID"])
		amiMsgChan <- m
	}
	GetAMI().RegisterHandler(event, &dh)
	go handlersMap[event](amiMsgChan, finishChan)
}

func init() {
	conf := conf.GetConf()
	ami = startAmi(conf.AsteriskHost, conf.AMILogin, conf.AMIPassword)
}
