package conf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/glog"
	"github.com/parnurzeal/gorequest"

	"github.com/warik/dialer/model"
)

const (
	REQUEST_TIMEOUT       = 5
	CDR_READ_INTERVAL     = 30 * time.Second
	QUEUE_RENEW_INTERVAL  = 10 * time.Minute
	NUMBERS_LOAD_INTERVAL = 5 * time.Minute
	REMOTE_ERROR_TEXT     = "Error on remote server, status code - %v"
	CDR_DB_FILE           = "cdr_log.db"
	MAX_CDR_NUMBER        = 50
	CDR_SAVERS_COUNT      = 5
	BOLT_CDR_BUCKET       = "CdrBucket"
	AMI_RECONNECT_TIMEOUT = 5 * time.Second
)

var conf Configuration

type PortalMap map[string]string

var PORTAL_MAP = map[string]PortalMap{
	"local": PortalMap{
		"ua": "http://my.example.com:5000/",
	},
	"trunk": PortalMap{
		"ua": "http://my.trunk.uaprom/",
		"ru": "http://my.ru-trunk.uaprom/",
	},
	"prod": PortalMap{
		"ua": "https://my.prom.ua/",
		"ru": "https://my.tiu.ru/",
		"by": "https://my.deal.by/",
		"kz": "https://my.satu.kz/",
	},
}
var ADMIN_PHONES = []string{"+380938677855", "+380637385529"}

type Configuration struct {
	AMILogin, Secret       string
	AMIPassword, Name      string
	AsteriskHost, Api      string
	Target, FolderForCalls string
	AllowedRemoteAddrs     []string
	Agencies               map[string]model.CountrySettings
	TimeZone               int
}

func (c Configuration) GetApi(country string, apiKey string) string {
	return PORTAL_MAP[conf.Target][country] + c.Api + apiKey
}

func initConf(confFile string) Configuration {
	path, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	file, err := os.Open(filepath.Join(path, confFile))
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
	for _, phone := range ADMIN_PHONES {
		params := fmt.Sprintf("login=%s&passwd=%s&destaddr=%s&msgchrset=cyr&msgtext=%s",
			"uaprominfo", "RVC18bfOLL", phone, msg)
		_, _, errs := gorequest.New().Get(url).Query(params).End()
		if len(errs) != 0 {
			for _, err := range errs {
				glog.Errorln(err)
			}
		}
	}
}

func init() {
	conf = initConf("conf.json")
}
