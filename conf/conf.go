package conf

import (
	"encoding/json"
	"log"
	"os"
)

var PORTAL_MAP = map[string]string{
	"ua": "http://my.example.com:5000/",
	// "ua": "https://my.prom.ua/",
	"ru": "https://my.tiu.ru/",
	"by": "https://my.deal.by/",
	"kz": "https://my.satu.kz/",
}

type Configuration struct {
	AsteriskHost, AMILogin string
	AMIPassword, Secret    string
	CompanyId, Api         string
	Countries              []string
}

func (c Configuration) GetApi(country string, apiKey string) string {
	return PORTAL_MAP[country] + c.Api + apiKey
}

func initConf(confFile string) Configuration {
	file, err := os.Open(confFile)
	if err != nil {
		log.Fatalln(err)
	}
	defer file.Close()

	conf := Configuration{}
	err = json.NewDecoder(file).Decode(&conf)
	if err != nil {
		log.Fatalln(err)
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
