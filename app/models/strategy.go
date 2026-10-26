package models

type TradeDecision struct {
	BuyVotes  int `json:"buy_votes"`
	SellVotes int `json:"sell_votes"`
}

// LatestTradeDecision receives completed candles only and shares all rules with backtests.
func (df *DataFrameCandle) LatestTradeDecision(params *TradeParams) TradeDecision {
	var result TradeDecision
	if params == nil || len(df.Candles) < 2 {
		return result
	}
	var decisions []func(int) (bool, bool)
	if params.EmaEnable {
		decisions = append(decisions, df.emaDecision(params.EmaPeriod1, params.EmaPeriod2))
	}
	if params.BbEnable {
		decisions = append(decisions, df.bbDecision(params.BbN, params.BbK))
	}
	if params.IchimokuEnable {
		decisions = append(decisions, df.ichimokuDecision())
	}
	if params.MacdEnable {
		decisions = append(decisions, df.macdDecision(params.MacdFastPeriod, params.MacdSlowPeriod, params.MacdSignalPeriod))
	}
	if params.RsiEnable {
		decisions = append(decisions, df.rsiDecision(params.RsiPeriod, params.RsiBuyThread, params.RsiSellThread))
	}
	for _, decision := range decisions {
		if decision != nil {
			buy, sell := decision(len(df.Candles) - 1)
			if buy {
				result.BuyVotes++
			}
			if sell {
				result.SellVotes++
			}
		}
	}
	return result
}

func (params *TradeParams) Enabled() bool {
	return params != nil && (params.EmaEnable || params.BbEnable || params.IchimokuEnable || params.MacdEnable || params.RsiEnable)
}
