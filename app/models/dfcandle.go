package models

import (
	"encoding/json"
	"math"
	"time"

	"github.com/markcheno/go-talib"

	"gotrading/tradingalgo"
)

// Series keeps unavailable values as NaN for calculations and emits them as JSON null.
type Series []float64

func (values Series) MarshalJSON() ([]byte, error) {
	output := make([]*float64, len(values))
	for i := range values {
		if !math.IsNaN(values[i]) && !math.IsInf(values[i], 0) {
			output[i] = &values[i]
		}
	}
	return json.Marshal(output)
}

func (values *Series) UnmarshalJSON(data []byte) error {
	var input []*float64
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	*values = make(Series, len(input))
	for i, value := range input {
		(*values)[i] = math.NaN()
		if value != nil {
			(*values)[i] = *value
		}
	}
	return nil
}

type DataFrameCandle struct {
	ProductCode   string         `json:"product_code"`
	Duration      time.Duration  `json:"duration"`
	Candles       []Candle       `json:"candles"`
	Smas          []Sma          `json:"smas,omitempty"`
	Emas          []Ema          `json:"emas,omitempty"`
	BBands        *BBands        `json:"bbands,omitempty"`
	IchimokuCloud *IchimokuCloud `json:"ichimoku,omitempty"`
	Rsi           *Rsi           `json:"rsi,omitempty"`
	Macd          *Macd          `json:"macd,omitempty"`
	Hvs           []Hv           `json:"hvs,omitempty"`
}

type Sma struct {
	Period int    `json:"period"`
	Values Series `json:"values"`
}
type Ema struct {
	Period int    `json:"period"`
	Values Series `json:"values"`
}
type BBands struct {
	N    int     `json:"n"`
	K    float64 `json:"k"`
	Up   Series  `json:"up"`
	Mid  Series  `json:"mid"`
	Down Series  `json:"down"`
}
type IchimokuCloud struct {
	Tenkan       Series `json:"tenkan"`
	Kijun        Series `json:"kijun"`
	SenkouA      Series `json:"senkoua"`
	SenkouB      Series `json:"senkoub"`
	Chikou       Series `json:"chikou"`
	Displacement int    `json:"displacement"`
}
type Rsi struct {
	Period int    `json:"period"`
	Values Series `json:"values"`
}
type Macd struct {
	FastPeriod   int    `json:"fast_period"`
	SlowPeriod   int    `json:"slow_period"`
	SignalPeriod int    `json:"signal_period"`
	Macd         Series `json:"macd"`
	MacdSignal   Series `json:"macd_signal"`
	MacdHist     Series `json:"macd_hist"`
}
type Hv struct {
	Period int    `json:"period"`
	Values Series `json:"values"`
}

func withLookback(values []float64, lookback int) Series {
	for i := 0; i < lookback && i < len(values); i++ {
		values[i] = math.NaN()
	}
	return Series(values)
}

func validPeriod(period int) bool { return period >= 2 && period <= 1000 }

func (df *DataFrameCandle) AddSma(period int) bool {
	if !validPeriod(period) || len(df.Candles) < period {
		return false
	}
	df.Smas = append(df.Smas, Sma{Period: period, Values: withLookback(talib.Sma(df.Closes(), period), period-1)})
	return true
}

func (df *DataFrameCandle) AddEma(period int) bool {
	if !validPeriod(period) || len(df.Candles) < period {
		return false
	}
	df.Emas = append(df.Emas, Ema{Period: period, Values: withLookback(talib.Ema(df.Closes(), period), period-1)})
	return true
}

func (df *DataFrameCandle) AddBBands(n int, k float64) bool {
	if !validPeriod(n) || len(df.Candles) < n || k <= 0 || k > 10 || math.IsNaN(k) || math.IsInf(k, 0) {
		return false
	}
	up, mid, down := talib.BBands(df.Closes(), n, k, k, talib.SMA)
	df.BBands = &BBands{N: n, K: k, Up: withLookback(up, n-1), Mid: withLookback(mid, n-1), Down: withLookback(down, n-1)}
	return true
}

func (df *DataFrameCandle) AddIchimoku() bool {
	if len(df.Candles) < 9 {
		return false
	}
	tenkan, kijun, a, b, chikou := tradingalgo.IchimokuCloud(df.Highs(), df.Low(), df.Closes())
	df.IchimokuCloud = &IchimokuCloud{Tenkan: Series(tenkan), Kijun: Series(kijun), SenkouA: Series(a), SenkouB: Series(b), Chikou: Series(chikou), Displacement: tradingalgo.IchimokuDisplacement}
	return true
}

func (df *DataFrameCandle) AddRsi(period int) bool {
	if !validPeriod(period) || len(df.Candles) <= period {
		return false
	}
	df.Rsi = &Rsi{Period: period, Values: withLookback(talib.Rsi(df.Closes(), period), period)}
	return true
}

func (df *DataFrameCandle) AddMacd(fast, slow, signal int) bool {
	if !validPeriod(fast) || !validPeriod(slow) || !validPeriod(signal) || fast >= slow || len(df.Candles) < slow+signal-1 {
		return false
	}
	macd, signalValues, hist := tradingalgo.Macd(df.Closes(), fast, slow, signal)
	lookback := slow + signal - 2
	df.Macd = &Macd{FastPeriod: fast, SlowPeriod: slow, SignalPeriod: signal, Macd: withLookback(macd, lookback), MacdSignal: withLookback(signalValues, lookback), MacdHist: withLookback(hist, lookback)}
	return true
}

func (df *DataFrameCandle) AddHv(period int) bool {
	if !validPeriod(period) || len(df.Candles) <= period {
		return false
	}
	df.Hvs = append(df.Hvs, Hv{Period: period, Values: Series(tradingalgo.Hv(df.Closes(), period))})
	return true
}

func (df *DataFrameCandle) Times() []time.Time {
	values := make([]time.Time, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Time
	}
	return values
}

func (df *DataFrameCandle) Opens() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Open
	}
	return values
}

func (df *DataFrameCandle) Closes() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Close
	}
	return values
}

func (df *DataFrameCandle) Highs() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.High
	}
	return values
}

func (df *DataFrameCandle) Low() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Low
	}
	return values
}

func (df *DataFrameCandle) Volume() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Volume
	}
	return values
}
