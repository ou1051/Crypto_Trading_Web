package controllers

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gotrading/app/models"
	"gotrading/config"
)

func TestBacktestAndOptimizationAPI(t *testing.T) {
	setupApplication(t)
	config.Config.BackTest = true
	config.Config.NumRanking = 2
	stamp := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 180; i++ {
		price := 150 + 30*math.Sin(float64(i)*.13)
		if err := models.NewCandle("BTC_USD", time.Minute, stamp.Add(time.Duration(i)*time.Minute), price+.7, price, price+2, price-2, 1).Create(); err != nil {
			t.Fatal(err)
		}
	}
	for _, strategy := range []string{"ema", "bbands", "ichimoku", "macd", "rsi"} {
		w := httptest.NewRecorder()
		NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&events=true&strategy="+strategy, nil))
		var df models.DataFrameCandle
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &df) != nil || df.Events == nil || df.Backtest == nil || df.Backtest.Strategy != strategy || df.Backtest.Execution != "next_open" {
			t.Fatalf("strategy=%s code=%d body=%s", strategy, w.Code, w.Body)
		}
		if df.Backtest.Costs == nil || df.Backtest.Costs.FeeRate != config.Config.FeeRate || df.Backtest.Costs.SlippageRate != config.Config.SlippageRate || df.Backtest.Profit > df.Backtest.GrossProfit+1e-9 {
			t.Fatalf("research cost assumptions were not applied: %+v", df.Backtest)
		}
		for _, event := range df.Events.Signals {
			if event.ProductCode != "BTC_USD" {
				t.Fatal("wrong event product")
			}
		}
	}
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&optimize=true", nil))
	var df models.DataFrameCandle
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &df) != nil || df.Optimization == nil || len(df.Optimization.Rankings) != 5 || df.Events != nil {
		t.Fatalf("optimization failed: %d %s", w.Code, w.Body)
	}
	if df.Optimization.Policy == nil || df.Optimization.TrainingCount+df.Optimization.ValidationCount != len(df.Candles) || df.Optimization.BreakEvenRise <= 0 {
		t.Fatal("optimization omitted validation/cost metadata")
	}
	var count int
	if err := models.DbConnection.QueryRow(`SELECT COUNT(*) FROM signal_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("backtests persisted simulated trades", err)
	}
}

func TestEventsAPIHandlesEmptySavedDataAndInvalidParameters(t *testing.T) {
	setupApplication(t)
	for _, query := range []string{"events=true&strategy=saved", "events=true&strategy=ema", "events=true&optimize=true", "events=false&strategy=invalid"} {
		w := httptest.NewRecorder()
		NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&"+query, nil))
		if w.Code != 200 {
			t.Fatalf("empty data failed: %d %s", w.Code, w.Body)
		}
	}
	for _, query := range []string{"events=invalid", "optimize=invalid", "events=true&strategy=unknown", "events=true&strategy=ema&emaPeriod1=0", "events=true&strategy=ema&emaPeriod1=20&emaPeriod2=10", "events=true&strategy=bbands&bbandsK=NaN", "events=true&strategy=macd&macdPeriod1=30&macdPeriod2=12", "events=true&strategy=rsi&rsiBuy=80&rsiSell=20", "events=true&strategy=rsi&rsiSell=100"} {
		w := httptest.NewRecorder()
		NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&"+query, nil))
		if w.Code != 400 || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("invalid request accepted: %s code=%d", query, w.Code)
		}
	}
}
