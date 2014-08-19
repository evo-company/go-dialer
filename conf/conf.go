package conf

import (
	"encoding/json"
	"os"
)

type Configuration struct {
	AsteriskHost, AMILogin, AMIPassword string
}

func GetConf(confFile string) (*Configuration, error) {
	file, err := os.Open(confFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	conf := Configuration{}
	err = json.NewDecoder(file).Decode(&conf)
	if err != nil {
		return nil, err
	}
	return &conf, nil
}
