package config

import "gopkg.in/ini.v1"
import "log"
import "os"


type ConfigList struct {
	ApiKey string
	ApiSecret string 
}

var Config ConfigList

func init() {
	cfg, err := ini.Load("config.ini")
	if err != nil {
		log.Fatalf("Fail to read file: %v", err)
		os.Exit(1)
	}

	Config = ConfigList{
		ApiKey: cfg.Section("bitflyer").Key("api_key").String(),
		ApiSecret: cfg.Section("bitflyer").Key("api_secret").String(),
	}
}
