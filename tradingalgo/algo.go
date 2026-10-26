package tradingalgo

import (
	"math"

	"github.com/markcheno/go-talib"
)

const IchimokuDisplacement = 26

func missing(length int) []float64 {
	values := make([]float64, length)
	for i := range values {
		values[i] = math.NaN()
	}
	return values
}

// IchimokuCloud includes the current high/low and projects leading spans 26 bars forward.
func IchimokuCloud(highs, lows, closes []float64) (tenkan, kijun, senkouA, senkouB, chikou []float64) {
	length := len(closes)
	tenkan, kijun = missing(length), missing(length)
	senkouA, senkouB, chikou = missing(length+IchimokuDisplacement), missing(length+IchimokuDisplacement), missing(length)
	if len(highs) != length || len(lows) != length {
		return
	}
	midpoint := func(end, period int) float64 {
		high, low := highs[end-period+1], lows[end-period+1]
		for j := end - period + 2; j <= end; j++ {
			high, low = math.Max(high, highs[j]), math.Min(low, lows[j])
		}
		return (high + low) / 2
	}
	for i := range closes {
		if i >= 8 {
			tenkan[i] = midpoint(i, 9)
		}
		if i >= 25 {
			kijun[i] = midpoint(i, 26)
			senkouA[i+IchimokuDisplacement] = (tenkan[i] + kijun[i]) / 2
		}
		if i >= 51 {
			senkouB[i+IchimokuDisplacement] = midpoint(i, 52)
		}
		if i >= IchimokuDisplacement {
			chikou[i-IchimokuDisplacement] = closes[i]
		}
	}
	return
}

// Hv is the population standard deviation of log returns in percent per candle,
// matching the course's non-annualized scale. Results align with closing-price indexes.
func Hv(closes []float64, period int) []float64 {
	values := missing(len(closes))
	if period < 2 || len(closes) <= period {
		return values
	}
	returns := make([]float64, len(closes)-1)
	for i, price := range closes {
		if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return values
		}
		if i > 0 {
			returns[i-1] = math.Log(price) - math.Log(closes[i-1])
		}
	}
	deviations := talib.StdDev(returns, period, 100)
	for i := period; i < len(closes); i++ {
		values[i] = deviations[i-1]
	}
	return values
}

// Macd seeds both price EMAs at the slow EMA's first valid bar and seeds the
// signal EMA from valid differences only; warmup zeroes must never enter its mean.
func Macd(closes []float64, fast, slow, signal int) (macd, signalValues, hist []float64) {
	macd, signalValues, hist = missing(len(closes)), missing(len(closes)), missing(len(closes))
	if fast < 2 || slow <= fast || signal < 2 || len(closes) < slow+signal-1 {
		return
	}
	var fastEMA, slowEMA float64
	for i := 0; i < slow; i++ {
		slowEMA += closes[i] / float64(slow)
		if i >= slow-fast {
			fastEMA += closes[i] / float64(fast)
		}
	}
	differences := make([]float64, len(closes)-slow+1)
	differences[0] = fastEMA - slowEMA
	for i := slow; i < len(closes); i++ {
		fastEMA += (closes[i] - fastEMA) * (2.0 / float64(fast+1))
		slowEMA += (closes[i] - slowEMA) * (2.0 / float64(slow+1))
		differences[i-slow+1] = fastEMA - slowEMA
	}
	var signalEMA float64
	for i := 0; i < signal; i++ {
		signalEMA += differences[i] / float64(signal)
	}
	for i := signal - 1; i < len(differences); i++ {
		if i >= signal {
			signalEMA += (differences[i] - signalEMA) * (2.0 / float64(signal+1))
		}
		index := i + slow - 1
		macd[index], signalValues[index], hist[index] = differences[i], signalEMA, differences[i]-signalEMA
	}
	return
}
