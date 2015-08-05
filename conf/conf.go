package conf

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/parnurzeal/gorequest"

	"github.com/warik/go-dialer/model"
)

const (
	REQUEST_TIMEOUT           = 5
	CDR_READ_INTERVAL         = 30 * time.Second
	QUEUE_RENEW_INTERVAL      = 10 * time.Minute
	NUMBERS_LOAD_INTERVAL     = 5 * time.Minute
	PHONE_CALLS_SAVE_INTERVAL = 10 * time.Second
	REMOTE_ERROR_TEXT         = "Error on remote server, status code - %v"
	CDR_DB_FILE               = "cdr_log.db"
	MAX_CDR_NUMBER            = 50
	MAX_PHONE_CALLS_NUMBER    = 10
	CDR_SAVERS_COUNT          = 2
	PHONE_CALL_SENDERS_COUNT  = 2
	BOLT_CDR_BUCKET           = "CdrBucket"
	AMI_RECONNECT_TIMEOUT     = 5 * time.Second
)

var (
	conf      Configuration
	once      sync.Once
	config    = flag.String("config", "conf.json", "Config file name")
	smsAlerts = flag.Bool("sms_alerts", true, "Should send sms in emergency cases")

	PORTAL_MAP = map[string]PortalMap{
		"local": PortalMap{
			"ua": "http://my.example.com:5000/",
			"ru": "http://my.ru-trunk.uaprom/",
		},
		"trunk": PortalMap{
			"ua": "http://my.trunk.uaprom/",
			"ru": "http://my.ru-trunk.uaprom/",
			"kz": "http://my.kz-trunk.uaprom/",
			"by": "http://my.by-trunk.uaprom/",
		},
		"prod": PortalMap{
			"ua": "https://my.prom.ua/",
			"ru": "https://my.tiu.ru/",
			"by": "https://my.deal.by/",
			"kz": "https://my.satu.kz/",
		},
	}
	ADMIN_PHONES = []string{"+380938677855", "+380637385529"}
)

type PortalMap map[string]string

type Configuration struct {
	AMILogin, Secret       string
	AMIPassword, Name      string
	AsteriskHost, Api      string
	Target, FolderForCalls string
	AllowedRemoteAddrs     []string
	Agencies               map[string]model.CountrySettings
	TimeZone               int
	StorageSettings        map[string]string
	CallBackQueuePrefix    string
	CallBackQueueSufix     string
	QuestionaryUrl         string
}

func (c Configuration) GetApi(country string, apiKey string) string {
	return PORTAL_MAP[c.Target][country] + c.Api + apiKey
}

func (c Configuration) GetCallBackQueueSufix() string {
	if c.Target != "prod" {
		return "test"
	}
	return c.CallBackQueueSufix
}

func (c Configuration) GetCallBackQueue(country string) string {
	if c.Target != "prod" {
		return "Local/777@test"
	}
	prefix, sufix := c.CallBackQueuePrefix, c.CallBackQueueSufix
	return fmt.Sprintf("%s@%s%s", prefix, country, sufix)
}

func (c Configuration) GetReviewUri(uniqueId string) string {
	return fmt.Sprintf("%s?callid=%s", c.QuestionaryUrl, uniqueId)
}

func InitConf() {
	path, _ := filepath.Abs(filepath.Dir(os.Args[0]))
	file, err := os.Open(filepath.Join(path, *config))
	if err != nil {
		panic(err)
	}
	defer file.Close()

	conf = Configuration{}
	err = json.NewDecoder(file).Decode(&conf)
	if err != nil {
		panic(err)
	}
}

func GetConf() *Configuration {
	once.Do(InitConf)
	return &conf
}

func Alert(msg string) {
	if !*smsAlerts {
		return
	}
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
