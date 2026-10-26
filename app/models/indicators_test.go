package models

import (
	"encoding/json"
	"math"
	"testing"
)

func priceFrame(prices ...float64) *DataFrameCandle {
	df := &DataFrameCandle{}
	for _, price := range prices {
		df.Candles = append(df.Candles, Candle{Open: price, Close: price, High: price + 1, Low: price - 1})
	}
	return df
}

func closeTo(t *testing.T, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > 1e-9 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestMovingAveragesAndBollingerBands(t *testing.T) {
	df := priceFrame(1, 2, 3, 4, 5)
	if !df.AddSma(3) || !df.AddBBands(3, 2) {
		t.Fatal("indicator did not calculate")
	}
	closeTo(t, df.Smas[0].Values[2], 2)
	closeTo(t, df.Smas[0].Values[4], 4)
	closeTo(t, df.BBands.Up[2], 2+2*math.Sqrt(2.0/3))
	closeTo(t, df.BBands.Down[2], 2-2*math.Sqrt(2.0/3))
	if !math.IsNaN(df.Smas[0].Values[1]) {
		t.Error("warmup must be unavailable")
	}
	df = priceFrame(2, 4, 8, 16, 32)
	if !df.AddEma(3) {
		t.Fatal("EMA did not calculate")
	}
	closeTo(t, df.Emas[0].Values[2], 14.0/3)
	closeTo(t, df.Emas[0].Values[3], 31.0/3)
}

func TestRSIWilderSmoothingAndMACD(t *testing.T) {
	df := priceFrame(10, 11, 9, 12, 11)
	if !df.AddRsi(3) {
		t.Fatal("RSI did not calculate")
	}
	closeTo(t, df.Rsi.Values[3], 200.0/3)
	closeTo(t, df.Rsi.Values[4], 160.0/3)
	df = priceFrame(1, 2, 3, 4, 5, 6)
	if !df.AddMacd(2, 3, 2) {
		t.Fatal("MACD did not calculate")
	}
	if !math.IsNaN(df.Macd.Macd[2]) {
		t.Error("MACD lookback is wrong")
	}
	for i := 3; i < 6; i++ {
		closeTo(t, df.Macd.Macd[i], .5)
		closeTo(t, df.Macd.MacdSignal[i], .5)
		closeTo(t, df.Macd.MacdHist[i], 0)
	}
}

func TestIndicatorBoundariesAndJSONNulls(t *testing.T) {
	for length := 0; length <= 60; length++ {
		prices := make([]float64, length)
		for i := range prices {
			prices[i] = float64(i + 100)
		}
		df := priceFrame(prices...)
		df.AddSma(7)
		df.AddEma(7)
		df.AddBBands(20, 2.5)
		df.AddIchimoku()
		df.AddRsi(14)
		df.AddMacd(12, 26, 9)
		df.AddHv(21)
		if _, err := json.Marshal(df); err != nil {
			t.Fatalf("length %d failed JSON encoding: %v", length, err)
		}
	}
	df := priceFrame(1, 2, 3)
	if !df.AddSma(3) || df.AddSma(0) || df.AddEma(-1) || df.AddBBands(2, math.NaN()) || df.AddMacd(3, 2, 2) || df.AddHv(1001) {
		t.Error("invalid period or exact lookback boundary was mishandled")
	}
	values := Series{math.NaN(), 0, 1.5}
	data, err := json.Marshal(values)
	if err != nil || string(data) != "[null,0,1.5]" {
		t.Fatalf("data=%s err=%v", data, err)
	}
	var decoded Series
	if err := json.Unmarshal(data, &decoded); err != nil || !math.IsNaN(decoded[0]) || decoded[1] != 0 {
		t.Fatalf("nulls did not roundtrip: %v err=%v", decoded, err)
	}
}
