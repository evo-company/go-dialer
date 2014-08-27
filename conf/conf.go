package conf

import (
	"encoding/json"
	"log"
	"os"
)

type Configuration struct {
	AsteriskHost, AMILogin string
	AMIPassword, Secret    string
	AgencyId, Api          string
	Portals                map[string]string
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
