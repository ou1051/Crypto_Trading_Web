package config

import (
	"fmt"
	"strconv"
	"time"

	"gopkg.in/ini.v1"
)

type ConfigList struct {
	ApiKey              string
	ApiSecret           string
	LogFile             string
	ProductCode         string
	TradeDuration       time.Duration
	Durations           map[string]time.Duration
	DbName              string
	SQLDriver           string
	Port                int
	BackTest            bool
	UsePercent          float64
	DataLimit           int
	StopLimitPercent    float64
	NumRanking          int
	PaperBalance        float64
	FeeRate             float64
	SpreadRate          float64
	SlippageRate        float64
	ValidationRatio     float64
	MinValidationTrades int
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
	section := cfg.Section("gotrading")
	settings.BackTest, err = strconv.ParseBool(section.Key("back_test").MustString("true"))
	if err != nil {
		return ConfigList{}, fmt.Errorf("back_test must be true or false")
	}
	settings.UsePercent, err = strconv.ParseFloat(section.Key("use_percent").MustString("0.9"), 64)
	if err != nil || !(settings.UsePercent > 0 && settings.UsePercent <= 1) {
		return ConfigList{}, fmt.Errorf("use_percent must be greater than 0 and at most 1")
	}
	settings.StopLimitPercent, err = strconv.ParseFloat(section.Key("stop_limit_percent").MustString("0.9"), 64)
	if err != nil || !(settings.StopLimitPercent > 0 && settings.StopLimitPercent <= 1) {
		return ConfigList{}, fmt.Errorf("stop_limit_percent must be greater than 0 and at most 1")
	}
	settings.DataLimit, err = strconv.Atoi(section.Key("data_limit").MustString("365"))
	if err != nil || settings.DataLimit < 2 || settings.DataLimit > 1000 {
		return ConfigList{}, fmt.Errorf("data_limit must be between 2 and 1000")
	}
	settings.NumRanking, err = strconv.Atoi(section.Key("num_ranking").MustString("3"))
	if err != nil || settings.NumRanking < 1 || settings.NumRanking > 5 {
		return ConfigList{}, fmt.Errorf("num_ranking must be between 1 and 5")
	}
	settings.PaperBalance, err = strconv.ParseFloat(section.Key("paper_balance").MustString("1000000"), 64)
	if err != nil || !(settings.PaperBalance > 0 && settings.PaperBalance <= 1e15) {
		return ConfigList{}, fmt.Errorf("paper_balance must be greater than zero and at most 1e15")
	}
	settings.FeeRate, err = strconv.ParseFloat(section.Key("fee_rate").MustString("0.0012"), 64)
	if err != nil || !(settings.FeeRate >= 0 && settings.FeeRate <= .1) {
		return ConfigList{}, fmt.Errorf("fee_rate must be between 0 and 0.1")
	}
	settings.SpreadRate, err = strconv.ParseFloat(section.Key("spread_rate").MustString("0.0005"), 64)
	if err != nil || !(settings.SpreadRate >= 0 && settings.SpreadRate <= .1) {
		return ConfigList{}, fmt.Errorf("spread_rate must be between 0 and 0.1")
	}
	settings.SlippageRate, err = strconv.ParseFloat(section.Key("slippage_rate").MustString("0.0005"), 64)
	if err != nil || !(settings.SlippageRate >= 0 && settings.SlippageRate <= .1) {
		return ConfigList{}, fmt.Errorf("slippage_rate must be between 0 and 0.1")
	}
	settings.ValidationRatio, err = strconv.ParseFloat(section.Key("validation_ratio").MustString("0.3"), 64)
	if err != nil || !(settings.ValidationRatio > 0 && settings.ValidationRatio < 1) {
		return ConfigList{}, fmt.Errorf("validation_ratio must be greater than 0 and less than 1")
	}
	settings.MinValidationTrades, err = strconv.Atoi(section.Key("min_validation_trades").MustString("2"))
	if err != nil || settings.MinValidationTrades < 1 || settings.MinValidationTrades > 1000 {
		return ConfigList{}, fmt.Errorf("min_validation_trades must be between 1 and 1000")
	}
	return settings, nil
}
