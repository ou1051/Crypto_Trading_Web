package models

import (
	"math"
	"sort"
	"time"
)

type BacktestResult struct {
	Strategy        string             `json:"strategy"`
	Parameters      map[string]float64 `json:"parameters"`
	Available       bool               `json:"available"`
	Execution       string             `json:"execution"`
	Size            float64            `json:"size"`
	Profit          float64            `json:"profit"`
	CompletedTrades int                `json:"completed_trades"`
}

type TradeParams struct {
	EmaEnable        bool    `json:"ema_enable"`
	EmaPeriod1       int     `json:"ema_period1"`
	EmaPeriod2       int     `json:"ema_period2"`
	BbEnable         bool    `json:"bb_enable"`
	BbN              int     `json:"bb_n"`
	BbK              float64 `json:"bb_k"`
	IchimokuEnable   bool    `json:"ichimoku_enable"`
	MacdEnable       bool    `json:"macd_enable"`
	MacdFastPeriod   int     `json:"macd_fast_period"`
	MacdSlowPeriod   int     `json:"macd_slow_period"`
	MacdSignalPeriod int     `json:"macd_signal_period"`
	RsiEnable        bool    `json:"rsi_enable"`
	RsiPeriod        int     `json:"rsi_period"`
	RsiBuyThread     float64 `json:"rsi_buy_threshold"`
	RsiSellThread    float64 `json:"rsi_sell_threshold"`
}

type Ranking struct {
	Strategy        string             `json:"strategy"`
	Enable          bool               `json:"enable"`
	Available       bool               `json:"available"`
	Performance     float64            `json:"performance"`
	CompletedTrades int                `json:"completed_trades"`
	Parameters      map[string]float64 `json:"parameters"`
}

type OptimizationResult struct {
	Rankings     []Ranking    `json:"rankings"`
	Parameters   *TradeParams `json:"parameters"`
	BestStrategy string       `json:"best_strategy"`
	CandleCount  int          `json:"candle_count"`
	AsOf         time.Time    `json:"as_of"`
}

func DefaultBacktestParams(strategy string) map[string]float64 {
	switch strategy {
	case "ema":
		return map[string]float64{"emaPeriod1": 7, "emaPeriod2": 14}
	case "bbands":
		return map[string]float64{"bbandsN": 20, "bbandsK": 2}
	case "ichimoku":
		return map[string]float64{}
	case "macd":
		return map[string]float64{"macdPeriod1": 12, "macdPeriod2": 26, "macdPeriod3": 9}
	case "rsi":
		return map[string]float64{"rsiPeriod": 14, "rsiBuy": 30, "rsiSell": 70}
	}
	return nil
}

func finite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}

// Signals use a completed candle; fills use only the following candle's known open.
// The last candle's changing close is never used to make a trade decision.
func (df *DataFrameCandle) simulate(decision func(int) (bool, bool)) *SignalEvents {
	if !validProductCode.MatchString(df.ProductCode) || df.Duration <= 0 || len(df.Candles) < 3 {
		return nil
	}
	for i, candle := range df.Candles {
		if candle.Time.IsZero() || !validAmount(candle.Open) || !validAmount(candle.Close) || !validAmount(candle.High) || !validAmount(candle.Low) || (i > 0 && !df.Candles[i-1].Time.Before(candle.Time)) {
			return nil
		}
	}
	events := NewSignalEvents()
	for i := 1; i+1 < len(df.Candles); i++ {
		buy, sell := decision(i)
		fill := df.Candles[i+1]
		if buy {
			events.Buy(df.ProductCode, fill.Time, fill.Open, 1, false)
		} else if sell {
			events.Sell(df.ProductCode, fill.Time, fill.Open, 1, false)
		}
	}
	return events
}

func (df *DataFrameCandle) indicatorFrame() *DataFrameCandle {
	return &DataFrameCandle{ProductCode: df.ProductCode, Duration: df.Duration, Candles: df.Candles}
}

func (df *DataFrameCandle) emaDecision(short, long int) func(int) (bool, bool) {
	if short >= long {
		return nil
	}
	indicators := df.indicatorFrame()
	if !indicators.AddEma(short) || !indicators.AddEma(long) {
		return nil
	}
	a, b := indicators.Emas[0].Values, indicators.Emas[1].Values
	return func(i int) (bool, bool) {
		if !finite(a[i-1], b[i-1], a[i], b[i]) {
			return false, false
		}
		return a[i-1] < b[i-1] && a[i] >= b[i], a[i-1] > b[i-1] && a[i] <= b[i]
	}
}

func (df *DataFrameCandle) BackTestEma(short, long int) *SignalEvents {
	decision := df.emaDecision(short, long)
	if decision == nil {
		return nil
	}
	return df.simulate(decision)
}

func (df *DataFrameCandle) bbDecision(n int, k float64) func(int) (bool, bool) {
	indicators := df.indicatorFrame()
	if !indicators.AddBBands(n, k) {
		return nil
	}
	up, down := indicators.BBands.Up, indicators.BBands.Down
	return func(i int) (bool, bool) {
		if !finite(up[i-1], up[i], down[i-1], down[i]) {
			return false, false
		}
		previous, current := df.Candles[i-1].Close, df.Candles[i].Close
		return previous < down[i-1] && current >= down[i], previous > up[i-1] && current <= up[i]
	}
}

func (df *DataFrameCandle) BackTestBb(n int, k float64) *SignalEvents {
	decision := df.bbDecision(n, k)
	if decision == nil {
		return nil
	}
	return df.simulate(decision)
}

func (df *DataFrameCandle) ichimokuDecision() func(int) (bool, bool) {
	if len(df.Candles) < 78 {
		return nil
	}
	indicators := df.indicatorFrame()
	if !indicators.AddIchimoku() {
		return nil
	}
	cloud := indicators.IchimokuCloud
	return func(i int) (bool, bool) {
		if i < 27 || !finite(cloud.Tenkan[i], cloud.Kijun[i], cloud.SenkouA[i], cloud.SenkouB[i]) {
			return false, false
		}
		previous, current := df.Candles[i-1], df.Candles[i]
		// Plotted Chikou[i] contains close[i+26]. Compare today's close with the
		// historical candle at i-26 instead, keeping the decision causal.
		pastPrevious, pastCurrent := df.Candles[i-27], df.Candles[i-26]
		buy := previous.Close < pastPrevious.High && current.Close >= pastCurrent.High && cloud.SenkouA[i] < current.Low && cloud.SenkouB[i] < current.Low && cloud.Tenkan[i] > cloud.Kijun[i]
		sell := previous.Close > pastPrevious.Low && current.Close <= pastCurrent.Low && cloud.SenkouA[i] > current.High && cloud.SenkouB[i] > current.High && cloud.Tenkan[i] < cloud.Kijun[i]
		return buy, sell
	}
}

func (df *DataFrameCandle) BackTestIchimoku() *SignalEvents {
	decision := df.ichimokuDecision()
	if decision == nil {
		return nil
	}
	return df.simulate(decision)
}

func (df *DataFrameCandle) macdDecision(fast, slow, signal int) func(int) (bool, bool) {
	indicators := df.indicatorFrame()
	if !indicators.AddMacd(fast, slow, signal) {
		return nil
	}
	a, b := indicators.Macd.Macd, indicators.Macd.MacdSignal
	return func(i int) (bool, bool) {
		if !finite(a[i-1], a[i], b[i-1], b[i]) {
			return false, false
		}
		return a[i] < 0 && b[i] < 0 && a[i-1] < b[i-1] && a[i] >= b[i], a[i] > 0 && b[i] > 0 && a[i-1] > b[i-1] && a[i] <= b[i]
	}
}

func (df *DataFrameCandle) BackTestMacd(fast, slow, signal int) *SignalEvents {
	decision := df.macdDecision(fast, slow, signal)
	if decision == nil {
		return nil
	}
	return df.simulate(decision)
}

func (df *DataFrameCandle) rsiDecision(period int, buyThreshold, sellThreshold float64) func(int) (bool, bool) {
	if !finite(buyThreshold, sellThreshold) || buyThreshold <= 0 || buyThreshold >= sellThreshold || sellThreshold >= 100 {
		return nil
	}
	indicators := df.indicatorFrame()
	if !indicators.AddRsi(period) {
		return nil
	}
	values := indicators.Rsi.Values
	return func(i int) (bool, bool) {
		if !finite(values[i-1], values[i]) {
			return false, false
		}
		return values[i-1] < buyThreshold && values[i] >= buyThreshold, values[i-1] > sellThreshold && values[i] <= sellThreshold
	}
}

func (df *DataFrameCandle) BackTestRsi(period int, buyThreshold, sellThreshold float64) *SignalEvents {
	decision := df.rsiDecision(period, buyThreshold, sellThreshold)
	if decision == nil {
		return nil
	}
	return df.simulate(decision)
}

func (df *DataFrameCandle) RunBacktest(strategy string, params map[string]float64) *SignalEvents {
	switch strategy {
	case "ema":
		return df.BackTestEma(int(params["emaPeriod1"]), int(params["emaPeriod2"]))
	case "bbands":
		return df.BackTestBb(int(params["bbandsN"]), params["bbandsK"])
	case "ichimoku":
		return df.BackTestIchimoku()
	case "macd":
		return df.BackTestMacd(int(params["macdPeriod1"]), int(params["macdPeriod2"]), int(params["macdPeriod3"]))
	case "rsi":
		return df.BackTestRsi(int(params["rsiPeriod"]), params["rsiBuy"], params["rsiSell"])
	}
	return nil
}

func (df *DataFrameCandle) SetBacktest(strategy string, params map[string]float64) {
	events := df.RunBacktest(strategy, params)
	available := events != nil
	if events == nil {
		events = NewSignalEvents()
	}
	df.Events = events
	df.Backtest = &BacktestResult{Strategy: strategy, Parameters: params, Available: available, Execution: "next_open", Size: 1, Profit: events.Profit(), CompletedTrades: events.CompletedTrades()}
}

func (df *DataFrameCandle) optimizeStrategy(strategy string) Ranking {
	best := Ranking{Strategy: strategy, Parameters: DefaultBacktestParams(strategy)}
	consider := func(params map[string]float64) {
		events := df.RunBacktest(strategy, params)
		if events == nil || events.CompletedTrades() == 0 || !finite(events.Profit()) {
			return
		}
		if !best.Available || events.Profit() > best.Performance {
			best.Available = true
			best.Performance = events.Profit()
			best.CompletedTrades = events.CompletedTrades()
			best.Parameters = params
		}
	}
	switch strategy {
	case "ema":
		for short := 5; short < 11; short++ {
			for long := 12; long < 20; long++ {
				consider(map[string]float64{"emaPeriod1": float64(short), "emaPeriod2": float64(long)})
			}
		}
	case "bbands":
		for n := 10; n < 20; n++ {
			for _, k := range []float64{1.9, 2.0} {
				consider(map[string]float64{"bbandsN": float64(n), "bbandsK": k})
			}
		}
	case "ichimoku":
		consider(map[string]float64{})
	case "macd":
		for fast := 10; fast < 19; fast++ {
			for slow := 20; slow < 30; slow++ {
				for signal := 5; signal < 15; signal++ {
					consider(map[string]float64{"macdPeriod1": float64(fast), "macdPeriod2": float64(slow), "macdPeriod3": float64(signal)})
				}
			}
		}
	case "rsi":
		for period := 5; period < 25; period++ {
			consider(map[string]float64{"rsiPeriod": float64(period), "rsiBuy": 30, "rsiSell": 70})
		}
	}
	return best
}

func (df *DataFrameCandle) OptimizeEma() (float64, int, int) {
	best := df.optimizeStrategy("ema")
	return best.Performance, int(best.Parameters["emaPeriod1"]), int(best.Parameters["emaPeriod2"])
}
func (df *DataFrameCandle) OptimizeBb() (float64, int, float64) {
	best := df.optimizeStrategy("bbands")
	return best.Performance, int(best.Parameters["bbandsN"]), best.Parameters["bbandsK"]
}
func (df *DataFrameCandle) OptimizeIchimoku() float64 {
	return df.optimizeStrategy("ichimoku").Performance
}
func (df *DataFrameCandle) OptimizeMacd() (float64, int, int, int) {
	best := df.optimizeStrategy("macd")
	return best.Performance, int(best.Parameters["macdPeriod1"]), int(best.Parameters["macdPeriod2"]), int(best.Parameters["macdPeriod3"])
}
func (df *DataFrameCandle) OptimizeRsi() (float64, int, float64, float64) {
	best := df.optimizeStrategy("rsi")
	return best.Performance, int(best.Parameters["rsiPeriod"]), best.Parameters["rsiBuy"], best.Parameters["rsiSell"]
}

func (df *DataFrameCandle) OptimizeStrategies(numRanking int) *OptimizationResult {
	numRanking = max(0, min(5, numRanking))
	result := &OptimizationResult{Rankings: make([]Ranking, 0, 5), Parameters: &TradeParams{}, CandleCount: len(df.Candles)}
	if len(df.Candles) > 0 {
		result.AsOf = df.Candles[len(df.Candles)-1].Time
	}
	for _, strategy := range []string{"ema", "bbands", "macd", "ichimoku", "rsi"} {
		result.Rankings = append(result.Rankings, df.optimizeStrategy(strategy))
	}
	sort.SliceStable(result.Rankings, func(i, j int) bool {
		a, b := result.Rankings[i], result.Rankings[j]
		if a.Available != b.Available {
			return a.Available
		}
		return a.Performance > b.Performance
	})
	for i := range result.Rankings {
		rank := &result.Rankings[i]
		rank.Enable = i < numRanking && rank.Available && rank.Performance > 0
		if rank.Enable && result.BestStrategy == "" {
			result.BestStrategy = rank.Strategy
		}
		params := rank.Parameters
		switch rank.Strategy {
		case "ema":
			result.Parameters.EmaEnable = rank.Enable
			result.Parameters.EmaPeriod1 = int(params["emaPeriod1"])
			result.Parameters.EmaPeriod2 = int(params["emaPeriod2"])
		case "bbands":
			result.Parameters.BbEnable = rank.Enable
			result.Parameters.BbN = int(params["bbandsN"])
			result.Parameters.BbK = params["bbandsK"]
		case "ichimoku":
			result.Parameters.IchimokuEnable = rank.Enable
		case "macd":
			result.Parameters.MacdEnable = rank.Enable
			result.Parameters.MacdFastPeriod = int(params["macdPeriod1"])
			result.Parameters.MacdSlowPeriod = int(params["macdPeriod2"])
			result.Parameters.MacdSignalPeriod = int(params["macdPeriod3"])
		case "rsi":
			result.Parameters.RsiEnable = rank.Enable
			result.Parameters.RsiPeriod = int(params["rsiPeriod"])
			result.Parameters.RsiBuyThread = params["rsiBuy"]
			result.Parameters.RsiSellThread = params["rsiSell"]
		}
	}
	return result
}

func (df *DataFrameCandle) OptimizeParams(rankings ...int) *TradeParams {
	limit := 3
	if len(rankings) > 0 {
		limit = rankings[0]
	}
	return df.OptimizeStrategies(limit).Parameters
}
