package config

import (
	"log"

	"gopkg.in/ini.v1"
)

type ConfigList struct {
	ApiKey      string
	ApiSecret   string
	LogFile     string
	ProductCode string
}

var Config ConfigList

func init() {
	cfg, err := ini.Load("config.ini")
	if err != nil {
		log.Fatalf("Fail to read file: %v", err)
	}

	Config = ConfigList{
		ApiKey:      cfg.Section("bitflyer").Key("api_key").String(),
		ApiSecret:   cfg.Section("bitflyer").Key("api_secret").String(),
		LogFile:     cfg.Section("gotrading").Key("log_file").String(),
		ProductCode: cfg.Section("gotrading").Key("product_code").String(),
	}
}
