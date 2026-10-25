package config

import (
	"fmt"
	"strconv"
	"time"

	"gopkg.in/ini.v1"
)

type ConfigList struct {
	ApiKey        string
	ApiSecret     string
	LogFile       string
	ProductCode   string
	TradeDuration time.Duration
	Durations     map[string]time.Duration
	DbName        string
	SQLDriver     string
	Port          int
}

var Config ConfigList

func Load(filename string) (ConfigList, error) {
	cfg, err := ini.Load(filename)
	if err != nil {
		return ConfigList{}, fmt.Errorf("load config: %w", err)
	}

	durations := map[string]time.Duration{"1s": time.Second, "1m": time.Minute, "1h": time.Hour}
	tradeDuration := cfg.Section("gotrading").Key("trade_duration").MustString("1m")
	if _, ok := durations[tradeDuration]; !ok {
		return ConfigList{}, fmt.Errorf("unsupported trade_duration %q", tradeDuration)
	}
	port, err := strconv.Atoi(cfg.Section("web").Key("port").MustString("8080"))
	if err != nil || port < 1 || port > 65535 {
		return ConfigList{}, fmt.Errorf("web port must be an integer between 1 and 65535")
	}
	settings := ConfigList{
		ApiKey:        cfg.Section("bitflyer").Key("api_key").String(),
		ApiSecret:     cfg.Section("bitflyer").Key("api_secret").String(),
		LogFile:       cfg.Section("gotrading").Key("log_file").MustString("gotrading.log"),
		ProductCode:   cfg.Section("gotrading").Key("product_code").MustString("BTC_JPY"),
		TradeDuration: durations[tradeDuration],
		Durations:     durations,
		DbName:        cfg.Section("db").Key("name").MustString("stockdata.sql"),
		SQLDriver:     cfg.Section("db").Key("driver").MustString("sqlite3"),
		Port:          port,
	}
	if settings.SQLDriver != "sqlite3" {
		return ConfigList{}, fmt.Errorf("unsupported database driver %q", settings.SQLDriver)
	}
	return settings, nil
}
