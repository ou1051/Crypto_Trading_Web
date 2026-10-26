package tradingalgo

import (
	"math"
	"testing"
)

func TestIchimokuUsesHighsAndLowsAndCorrectOffsets(t *testing.T) {
	highs, lows, closes := make([]float64, 80), make([]float64, 80), make([]float64, 80)
	for i := range closes {
		highs[i], lows[i], closes[i] = 100+float64(i), 80+float64(i), 89+float64(i)
	}
	tenkan, kijun, a, b, chikou := IchimokuCloud(highs, lows, closes)
	if !math.IsNaN(tenkan[7]) || tenkan[8] != 94 || kijun[25] != 102.5 {
		t.Fatalf("incorrect high/low windows: tenkan=%v kijun=%v", tenkan[8], kijun[25])
	}
	if !math.IsNaN(a[50]) || a[51] != 106.75 || !math.IsNaN(b[76]) || b[77] != 115.5 {
		t.Fatalf("incorrect leading displacement: A=%v B=%v", a[51], b[77])
	}
	if len(a) != 106 || math.IsNaN(a[105]) || chikou[0] != closes[26] || !math.IsNaN(chikou[54]) {
		t.Error("leading and lagging spans are incorrectly aligned")
	}
}

func TestHistoricalVolatilityUsesLogReturnsAndMatchesCandleIndexes(t *testing.T) {
	values := Hv([]float64{100, 110, 99, 118.8}, 2)
	if len(values) != 4 || !math.IsNaN(values[0]) || !math.IsNaN(values[1]) {
		t.Fatalf("incorrect warmup: %v", values)
	}
	want := []float64{math.Abs(math.Log(1.1)-math.Log(.9)) * 50, math.Abs(math.Log(.9)-math.Log(1.2)) * 50}
	for i := range want {
		if math.Abs(values[i+2]-want[i]) > 1e-9 {
			t.Fatalf("HV[%d]=%v want=%v", i+2, values[i+2], want[i])
		}
	}
	for _, values := range [][]float64{nil, {100}, {100, 0, 110}, {100, -1, 110}} {
		for _, value := range Hv(values, 2) {
			if !math.IsNaN(value) {
				t.Error("invalid or insufficient input must not produce a value")
			}
		}
	}
}

func TestMACDSignalSeedDoesNotIncludeWarmupZeroes(t *testing.T) {
	macd, signal, hist := Macd([]float64{1, 2, 1, 4, 2, 5}, 2, 3, 2)
	for _, check := range []struct{ got, want float64 }{{macd[3], .5}, {signal[3], 1.0 / 3}, {hist[3], 1.0 / 6}, {macd[4], 1.0 / 18}, {signal[4], 4.0 / 27}, {hist[4], -5.0 / 54}} {
		if math.Abs(check.got-check.want) > 1e-9 {
			t.Fatalf("got=%v want=%v", check.got, check.want)
		}
	}
	if !math.IsNaN(macd[2]) || !math.IsNaN(signal[2]) {
		t.Error("MACD warmup must be unavailable")
	}
}
