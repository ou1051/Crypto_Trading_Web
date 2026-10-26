package models

// TradingCosts are assumptions for research, not a prediction of live execution.
// SpreadRate is the full bid/ask spread relative to the candle midpoint.
type TradingCosts struct {
	FeeRate      float64 `json:"fee_rate"`
	SpreadRate   float64 `json:"spread_rate"`
	SlippageRate float64 `json:"slippage_rate"`
}

func (costs TradingCosts) Valid() bool {
	return finite(costs.FeeRate, costs.SpreadRate, costs.SlippageRate) &&
		costs.FeeRate >= 0 && costs.FeeRate <= .1 && costs.SpreadRate >= 0 && costs.SpreadRate <= .1 &&
		costs.SlippageRate >= 0 && costs.SlippageRate <= .1
}

// BreakEvenRise is the midpoint rise required for a modeled round trip to break even.
func (costs TradingCosts) BreakEvenRise() float64 {
	if !costs.Valid() {
		return 0
	}
	buy := (1 + costs.SpreadRate/2) * (1 + costs.SlippageRate) / (1 - costs.FeeRate)
	sell := (1 - costs.SpreadRate/2) * (1 - costs.SlippageRate) / (1 + costs.FeeRate)
	return buy/sell - 1
}

type StrategyPolicy struct {
	Costs               TradingCosts `json:"costs"`
	ValidationRatio     float64      `json:"validation_ratio"`
	MinValidationTrades int          `json:"min_validation_trades"`
	DataLimit           int          `json:"data_limit"`
	NumRanking          int          `json:"num_ranking"`
}

func (policy StrategyPolicy) Valid() bool {
	return policy.Costs.Valid() && finite(policy.ValidationRatio) && policy.ValidationRatio > 0 && policy.ValidationRatio < 1 &&
		policy.MinValidationTrades >= 1 && policy.MinValidationTrades <= 1000 &&
		policy.DataLimit >= 2 && policy.DataLimit <= 1000 && policy.NumRanking >= 1 && policy.NumRanking <= 5
}

func (df *DataFrameCandle) continuous() bool {
	if !validProductCode.MatchString(df.ProductCode) || df.Duration <= 0 || len(df.Candles) < 3 {
		return false
	}
	for i, candle := range df.Candles {
		if candle.Time.IsZero() || !validAmount(candle.Open) || !validAmount(candle.Close) || !validAmount(candle.High) || !validAmount(candle.Low) ||
			(i > 0 && candle.Time.Sub(df.Candles[i-1].Time) != df.Duration) {
			return false
		}
	}
	return true
}

// simulateCosts always starts flat and uses earlier candles only as indicator warmup.
// One gross BTC is bought per entry. Both commissions are paid in BTC, matching
// the spot-position accounting used by automation. No terminal close is invented.
func (df *DataFrameCandle) simulateCosts(decision func(int) (bool, bool), costs TradingCosts, firstFill int) *SignalEvents {
	if decision == nil || !costs.Valid() || !df.continuous() {
		return nil
	}
	events := NewSignalEvents()
	quantity := 1 - costs.FeeRate
	for i := max(1, firstFill-1); i+1 < len(df.Candles); i++ {
		buy, sell := decision(i)
		fill := df.Candles[i+1]
		var recorded bool
		var price, commission float64
		if buy {
			price = fill.Open * (1 + costs.SpreadRate/2) * (1 + costs.SlippageRate)
			commission = costs.FeeRate
			recorded = events.Buy(df.ProductCode, fill.Time, price/quantity, quantity, false)
		} else if sell {
			price = fill.Open * (1 - costs.SpreadRate/2) * (1 - costs.SlippageRate)
			commission = quantity / (1 + costs.FeeRate) * costs.FeeRate
			recorded = events.Sell(df.ProductCode, fill.Time, price/(1+costs.FeeRate), quantity, false)
		}
		if recorded && costs != (TradingCosts{}) {
			last := &events.Signals[len(events.Signals)-1]
			last.ExecutionPrice, last.Commission = price, commission
		}
	}
	return events
}

func (df *DataFrameCandle) strategyDecision(strategy string, params map[string]float64) func(int) (bool, bool) {
	switch strategy {
	case "ema":
		return df.emaDecision(int(params["emaPeriod1"]), int(params["emaPeriod2"]))
	case "bbands":
		return df.bbDecision(int(params["bbandsN"]), params["bbandsK"])
	case "ichimoku":
		return df.ichimokuDecision()
	case "macd":
		return df.macdDecision(int(params["macdPeriod1"]), int(params["macdPeriod2"]), int(params["macdPeriod3"]))
	case "rsi":
		return df.rsiDecision(int(params["rsiPeriod"]), params["rsiBuy"], params["rsiSell"])
	}
	return nil
}

func (df *DataFrameCandle) RunBacktestWithCosts(strategy string, params map[string]float64, costs TradingCosts) *SignalEvents {
	return df.simulateCosts(df.strategyDecision(strategy, params), costs, 2)
}
