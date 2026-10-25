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
	if settings.ApiKey != "XXXXX" || settings.ApiSecret != "XXXXX" || settings.Port != 8081 || settings.DbName != "test.sqlite" || settings.TradeDuration != time.Minute || len(settings.Durations) != 3 {
		t.Fatalf("unexpected settings: %+v", settings)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	for _, contents := range []string{"[web]\nport = abc", "[web]\nport = 0", "[web]\nport = 65536", "[gotrading]\ntrade_duration = 10m", "[db]\ndriver = mysql"} {
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
