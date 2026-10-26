package models

import (
	"fmt"
	"sort"
)

// OptimizeValidated tunes only on the earlier partition. The later partition
// starts with a flat portfolio and keeps earlier bars only for indicator warmup.
// Validation participates in strategy selection, so it is not a final blind test.
func (df *DataFrameCandle) OptimizeValidated(policy StrategyPolicy) (*OptimizationResult, error) {
	if !policy.Valid() {
		return nil, fmt.Errorf("invalid strategy evaluation policy")
	}
	split := max(0, min(len(df.Candles)-1, int(float64(len(df.Candles))*(1-policy.ValidationRatio))))
	result := &OptimizationResult{
		Rankings: make([]Ranking, 0, 5), Parameters: &TradeParams{}, CandleCount: len(df.Candles),
		Policy: &policy, TrainingCount: split, ValidationCount: len(df.Candles) - split, BreakEvenRise: policy.Costs.BreakEvenRise(),
	}
	if len(df.Candles) > 0 {
		result.AsOf = df.Candles[len(df.Candles)-1].Time
		result.ValidationStart = df.Candles[split].Time
	}
	training := df.indicatorFrame()
	training.Candles = df.Candles[:split]
	for _, strategy := range []string{"ema", "bbands", "macd", "ichimoku", "rsi"} {
		best := training.optimizeStrategy(strategy, policy.Costs)
		rank := Ranking{Strategy: strategy, Parameters: best.Parameters, TrainingProfit: best.Performance, TrainingTrades: best.CompletedTrades}
		if split >= 3 && len(df.Candles)-split >= 2 && best.Available {
			net := df.simulateCosts(df.strategyDecision(strategy, best.Parameters), policy.Costs, split)
			gross := df.simulateCosts(df.strategyDecision(strategy, best.Parameters), TradingCosts{}, split)
			if net != nil && gross != nil {
				rank.Performance, rank.GrossPerformance = net.Profit(), gross.Profit()
				rank.CompletedTrades = net.CompletedTrades()
				rank.Available = rank.CompletedTrades > 0
			}
		}
		result.Rankings = append(result.Rankings, rank)
	}
	eligible := func(rank Ranking) bool {
		return rank.Available && rank.TrainingProfit > 0 && rank.Performance > 0 && rank.CompletedTrades >= policy.MinValidationTrades
	}
	sort.SliceStable(result.Rankings, func(i, j int) bool {
		a, b := result.Rankings[i], result.Rankings[j]
		if eligible(a) != eligible(b) {
			return eligible(a)
		}
		if a.Available != b.Available {
			return a.Available
		}
		return a.Performance > b.Performance
	})
	for i := range result.Rankings {
		rank := &result.Rankings[i]
		rank.Enable = i < policy.NumRanking && eligible(*rank)
		if rank.Enable && result.BestStrategy == "" {
			result.BestStrategy = rank.Strategy
		}
		assignRank(result.Parameters, *rank)
	}
	return result, nil
}

func assignRank(target *TradeParams, rank Ranking) {
	params := rank.Parameters
	switch rank.Strategy {
	case "ema":
		target.EmaEnable = rank.Enable
		target.EmaPeriod1 = int(params["emaPeriod1"])
		target.EmaPeriod2 = int(params["emaPeriod2"])
	case "bbands":
		target.BbEnable = rank.Enable
		target.BbN = int(params["bbandsN"])
		target.BbK = params["bbandsK"]
	case "ichimoku":
		target.IchimokuEnable = rank.Enable
	case "macd":
		target.MacdEnable = rank.Enable
		target.MacdFastPeriod = int(params["macdPeriod1"])
		target.MacdSlowPeriod = int(params["macdPeriod2"])
		target.MacdSignalPeriod = int(params["macdPeriod3"])
	case "rsi":
		target.RsiEnable = rank.Enable
		target.RsiPeriod = int(params["rsiPeriod"])
		target.RsiBuyThread = params["rsiBuy"]
		target.RsiSellThread = params["rsiSell"]
	}
}
