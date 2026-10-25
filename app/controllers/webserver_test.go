package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"gotrading/app/models"
)

func seedCandles(t *testing.T) {
	t.Helper()
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	for _, duration := range []time.Duration{time.Second, time.Minute} {
		for i := 0; i < 3; i++ {
			candle := models.NewCandle("BTC_USD", duration, start.Add(time.Duration(i)*duration), 100+float64(i), 105+float64(i), 110+float64(i), 90+float64(i), 2)
			if err := candle.Create(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestCandleAPIReturnsLatestWindow(t *testing.T) {
	setupApplication(t)
	seedCandles(t)
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&duration=1m&limit=2", nil))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var df models.DataFrameCandle
	if err := json.Unmarshal(w.Body.Bytes(), &df); err != nil {
		t.Fatal(err)
	}
	if df.ProductCode != "BTC_USD" || df.Duration != time.Minute || len(df.Candles) != 2 || df.Candles[0].Open != 101 || df.Candles[1].Open != 102 || !df.Candles[0].Time.Before(df.Candles[1].Time) {
		t.Fatalf("unexpected dataframe: %+v", df)
	}
}

func TestCandleAPIValidatesRequestBeforeQuerying(t *testing.T) {
	setupApplication(t)
	for _, sample := range []struct {
		method, target string
		status         int
	}{
		{http.MethodGet, "/api/candle/", http.StatusBadRequest},
		{http.MethodGet, "/api/candle/?product_code=BTC_USD&duration=2m", http.StatusBadRequest},
		{http.MethodGet, "/api/candle/?product_code=BTC_USD&limit=-1", http.StatusBadRequest},
		{http.MethodGet, "/api/candle/?product_code=BTC_USD&limit=0", http.StatusBadRequest},
		{http.MethodGet, "/api/candle/?product_code=BTC_USD&limit=1001", http.StatusBadRequest},
		{http.MethodGet, "/api/candle/?product_code=BTC_USD&limit=abc", http.StatusBadRequest},
		{http.MethodGet, "/api/candle/?product_code=BTC_JPY", http.StatusBadRequest},
		{http.MethodGet, "/api/candle/?product_code=" + url.QueryEscape(`BTC_USD"; DROP TABLE signal_events; --`), http.StatusBadRequest},
		{http.MethodGet, "/api/candle/extra?product_code=BTC_USD", http.StatusNotFound},
		{http.MethodGet, "/api/unknown/", http.StatusNotFound},
		{http.MethodPost, "/api/candle/?product_code=BTC_USD", http.StatusMethodNotAllowed},
	} {
		t.Run(sample.method+sample.target, func(t *testing.T) {
			w := httptest.NewRecorder()
			NewRouter().ServeHTTP(w, httptest.NewRequest(sample.method, sample.target, nil))
			if w.Code != sample.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			var response JSONError
			decoder := json.NewDecoder(w.Body)
			if err := decoder.Decode(&response); err != nil || response.Code != sample.status || response.Error == "" {
				t.Fatalf("invalid error response: %+v err=%v", response, err)
			}
			if err := decoder.Decode(new(any)); err != io.EOF {
				t.Fatalf("handler continued writing after an error: %v", err)
			}
		})
	}
}

func TestEmptyCandleAPIAndChart(t *testing.T) {
	setupApplication(t)
	for _, target := range []string{"/api/candle/?product_code=BTC_USD", "/chart/"} {
		w := httptest.NewRecorder()
		NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("empty database must be supported: status=%d body=%s", w.Code, w.Body)
		}
		if strings.HasPrefix(target, "/api/") && !strings.Contains(w.Body.String(), `"candles":[]`) {
			t.Errorf("empty candles must be [], got %s", w.Body)
		}
		if target == "/chart/" && !strings.Contains(w.Body.String(), "/static/chart.js") {
			t.Error("chart must load data asynchronously")
		}
	}
}

func TestChartPassesSettingsToAjaxClient(t *testing.T) {
	setupApplication(t)
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/chart/?duration=1s&limit=2", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("chart did not render: status=%d body=%s", w.Code, w.Body)
	}
	for _, expected := range []string{`data-product-code="BTC_USD"`, `data-duration="1s" data-limit="2"`, `id="filter_div"`, `data-duration="1h"`, `src="/static/chart.js"`} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Errorf("chart is missing %q", expected)
		}
	}
}

func TestClosedDatabaseReturnsServerError(t *testing.T) {
	setupApplication(t)
	models.DbConnection.Close()
	for _, target := range []string{"/api/candle/?product_code=BTC_USD"} {
		w := httptest.NewRecorder()
		NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "database is closed") {
			t.Fatalf("unexpected database error response: status=%d body=%s", w.Code, w.Body)
		}
	}
	// The chart shell must still load so that its Ajax client can report API failures.
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/chart/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("chart shell must not query the database: status=%d body=%s", w.Code, w.Body)
	}
}

func TestEmbeddedChartScriptIsServed(t *testing.T) {
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/static/chart.js", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "javascript") || !strings.Contains(w.Body.String(), "ChartRangeFilter") {
		t.Fatalf("script was not served: status=%d type=%s", w.Code, w.Header().Get("Content-Type"))
	}
	w = httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/static/chart.html", nil))
	if w.Code != http.StatusNotFound {
		t.Error("static route must not expose templates")
	}
}

func TestRouterAndCanceledServer(t *testing.T) {
	setupApplication(t)
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/chart/" {
		t.Fatalf("unexpected root redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
	w = httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/chart/extra", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown chart path must return 404, got %d", w.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := StartWebServer(ctx); err != nil {
		t.Fatalf("canceled server must stop cleanly: %v", err)
	}
}
