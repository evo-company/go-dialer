package conf

import (
	"encoding/json"
	"os"
	"time"

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

var PORTAL_MAP = map[string]string{
	"ua": "http://my.example.com:5000/",
	// "ua": "https://my.prom.ua/",
	"ru": "https://my.tiu.ru/",
	"by": "https://my.deal.by/",
	"kz": "https://my.satu.kz/",
}

type Configuration struct {
	AsteriskHost, AMILogin   string
	AMIPassword, Secret, Api string
	AllowedRemoteAddrs       []string
	Agencies                 map[string]model.CountrySettings
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

var conf Configuration

func GetConf() *Configuration {
	return &conf
}

func init() {
	conf = initConf("conf.json")
}
