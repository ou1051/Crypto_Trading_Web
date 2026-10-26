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
)

func TestCandleAPIAddsRequestedIndicatorsWithAlignedValues(t *testing.T) {
	setupApplication(t)
	start := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 300; i++ {
		price := 100 + float64(i) + math.Sin(float64(i))
		if err := models.NewCandle("BTC_USD", time.Minute, start.Add(time.Duration(i)*time.Minute), price, price+.5, price+2, price-1, 2).Create(); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&sma=true&ema=true&bbands=true&bbandsK=2.5&ichimoku=true&rsi=true&macd=true&hv=true", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var df models.DataFrameCandle
	if err := json.Unmarshal(w.Body.Bytes(), &df); err != nil {
		t.Fatal(err)
	}
	if len(df.Smas) != 3 || len(df.Emas) != 3 || df.BBands == nil || df.BBands.K != 2.5 || df.IchimokuCloud == nil || df.Rsi == nil || df.Macd == nil || len(df.Hvs) != 3 {
		t.Fatalf("requested indicators missing: %s", w.Body)
	}
	if len(df.Smas[0].Values) != 300 || !math.IsNaN(df.Smas[0].Values[5]) || math.IsNaN(df.Smas[0].Values[6]) || len(df.Hvs[2].Values) != 300 || !math.IsNaN(df.Hvs[2].Values[251]) || math.IsNaN(df.Hvs[2].Values[252]) {
		t.Fatal("indicator timestamps or lookback alignment are incorrect")
	}
	if len(df.IchimokuCloud.SenkouA) != 326 || df.IchimokuCloud.Displacement != 26 {
		t.Fatal("Ichimoku projection missing")
	}
}

func TestCandleAPIRejectsInvalidIndicatorParameters(t *testing.T) {
	setupApplication(t)
	for _, query := range []string{"sma=true&smaPeriod1=0", "ema=true&emaPeriod2=-1", "hv=true&hvPeriod1=1001", "rsi=true&rsiPeriod=abc", "bbands=true&bbandsK=NaN", "bbands=true&bbandsK=Inf", "bbands=true&bbandsK=0", "bbands=true&bbandsN=1", "macd=true&macdPeriod1=26&macdPeriod2=12", "macd=true&macdPeriod3=0", "ichimoku=invalid", "sma=true&smaPeriod1="} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&"+query, nil))
			var response JSONError
			if w.Code != http.StatusBadRequest || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Code != 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
		})
	}
	for _, query := range []string{"sma=true&ema=true&bbands=true&ichimoku=true&rsi=true&macd=true&hv=true", "sma=false&smaPeriod1=invalid"} {
		w := httptest.NewRecorder()
		NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/candle/?product_code=BTC_USD&"+query, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"candles":[]`) {
			t.Fatalf("empty data or disabled flags must be supported: status=%d body=%s", w.Code, w.Body)
		}
	}
}
