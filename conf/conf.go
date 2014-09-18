package conf

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/parnurzeal/gorequest"

	"github.com/warik/dialer/model"
)

const (
	REQUEST_TIMEOUT       = 5
	CDR_READ_INTERVAL     = 30 * time.Second
	REMOTE_ERROR_TEXT     = "Error on remote server, status code - %v"
	CDR_DB_FILE           = "cdr_log.db"
	MAX_CDR_NUMBER        = 50
	BOLT_CDR_BUCKET       = "CdrBucket"
	AMI_READ_DEADLINE     = 0
	AMI_RECONNECT_TIMEOUT = 5 * time.Second
)

var conf Configuration

var PORTAL_MAP = map[string]string{
	"ua": "http://my.example.com:5000/",
	// "ua": "https://my.prom.ua/",
	"ru": "https://my.tiu.ru/",
	"by": "https://my.deal.by/",
	"kz": "https://my.satu.kz/",
}

type Configuration struct {
	AMILogin, Secret   string
	AMIPassword, Name  string
	AsteriskHost, Api  string
	AllowedRemoteAddrs []string
	Agencies           map[string]model.CountrySettings
}

func (c Configuration) GetApi(country string, apiKey string) string {
	return PORTAL_MAP[country] + c.Api + apiKey
}

func initConf(confFile string) Configuration {
	file, err := os.Open(confFile)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	conf := Configuration{}
	err = json.NewDecoder(file).Decode(&conf)
	if err != nil {
		panic(err)
	}
	return conf
}

func GetConf() *Configuration {
	return &conf
}

func Alert(msg string) {
	url := "http://sms.skysms.net/api/submit_sm"
	msg = fmt.Sprintf("%s: %s", conf.Name, msg)
	params := fmt.Sprintf("login=%s&passwd=%s&destaddr=%s&msgchrset=cyr&msgtext=%s",
		"uaprominfo", "RVC18bfOLL", "+380637385529", msg)
	_, _, errs := gorequest.New().Get(url).Query(params).End()
	if len(errs) != 0 {
		for _, err := range errs {
			log.Println(err)
		}
	}
}

func init() {
	conf = initConf("conf.json")
}
