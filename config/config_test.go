package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSettingsAndPreservePlaceholders(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.ini")
	if err := os.WriteFile(filename, []byte("[bitflyer]\napi_key = XXXXX\napi_secret = XXXXX\n[gotrading]\nproduct_code = BTC_USD\ntrade_duration = 1m\n[db]\nname = test.sqlite\ndriver = sqlite3\n[web]\nport = 8081\n"), 0600); err != nil {
		t.Fatal(err)
	}
	settings, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ApiKey != "XXXXX" || settings.ApiSecret != "XXXXX" || settings.Port != 8081 || settings.DbName != "test.sqlite" || settings.TradeDuration != time.Minute || len(settings.Durations) != 3 || !settings.BackTest || settings.DataLimit != 365 || settings.NumRanking != 3 || settings.UsePercent != .9 || settings.StopLimitPercent != .9 || settings.PaperBalance != 1000000 || settings.FeeRate != .0012 || settings.SpreadRate != .0005 || settings.SlippageRate != .0005 || settings.ValidationRatio != .3 || settings.MinValidationTrades != 2 {
		t.Fatalf("unexpected settings: %+v", settings)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	for _, contents := range []string{"[web]\nport = abc", "[web]\nport = 0", "[web]\nport = 65536", "[gotrading]\ntrade_duration = 10m", "[db]\ndriver = mysql", "[gotrading]\nback_test=invalid", "[gotrading]\nuse_percent=NaN", "[gotrading]\nstop_limit_percent=2", "[gotrading]\ndata_limit=0", "[gotrading]\nnum_ranking=6", "[gotrading]\npaper_balance=0", "[gotrading]\npaper_balance=Inf", "[gotrading]\nfee_rate=NaN", "[gotrading]\nfee_rate=-1", "[gotrading]\nfee_rate=.2"} {
		t.Run(contents, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "config.ini")
			if err := os.WriteFile(filename, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(filename); err == nil {
				t.Error("invalid configuration was accepted")
			}
		})
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.ini")); err == nil {
		t.Error("missing configuration was accepted")
	}
}

func TestLoadRejectsInvalidResearchSettings(t *testing.T) {
	for _, setting := range []string{"spread_rate=NaN", "spread_rate=-0.1", "spread_rate=0.11", "slippage_rate=Inf", "slippage_rate=-1", "validation_ratio=0", "validation_ratio=1", "validation_ratio=NaN", "min_validation_trades=0", "min_validation_trades=1001", "min_validation_trades=1.5"} {
		t.Run(setting, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "config.ini")
			if err := os.WriteFile(filename, []byte("[gotrading]\n"+setting), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(filename); err == nil {
				t.Fatal("invalid research setting was accepted")
			}
		})
	}
}
