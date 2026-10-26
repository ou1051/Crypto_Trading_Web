package models

import (
	"math"
	"reflect"
	"testing"
)

func evaluationPolicy() StrategyPolicy {
	return StrategyPolicy{Costs: TradingCosts{FeeRate: .0012, SpreadRate: .0005, SlippageRate: .0005}, ValidationRatio: .3, MinValidationTrades: 2, DataLimit: 365, NumRanking: 3}
}

func TestRoundTripCostsTurnAFlatMarketIntoALoss(t *testing.T) {
	df := waveFrame(5)
	for i := range df.Candles {
		df.Candles[i].Open = 100
	}
	decision := func(i int) (bool, bool) { return i == 1, i == 3 }
	gross := df.simulateCosts(decision, TradingCosts{}, 2)
	costs := TradingCosts{FeeRate: .01, SpreadRate: .02, SlippageRate: .01}
	net := df.simulateCosts(decision, costs, 2)
	if gross.Profit() != 0 || net.CompletedTrades() != 1 {
		t.Fatal("flat-market fixture did not settle a round trip")
	}
	// Buy 1 BTC at 102.01, retain 0.99 BTC, then sell 0.99/1.01 BTC
	// at 98.01 while spending the remainder on the sale's BTC commission.
	if math.Abs(net.Profit()-(-5.940792079207922)) > 1e-9 {
		t.Fatalf("incorrect net round trip: %.12f", net.Profit())
	}
	if math.Abs(net.Signals[0].ExecutionPrice-102.01) > 1e-9 || net.Signals[0].Size != .99 || net.Signals[0].Commission != .01 {
		t.Fatalf("incorrect buy fill: %+v", net.Signals[0])
	}
	// The modeled break-even rise must exactly offset all three cost components.
	df.Candles[4].Open = 100 * (1 + costs.BreakEvenRise())
	if got := df.simulateCosts(decision, costs, 2).Profit(); math.Abs(got) > 1e-9 {
		t.Fatalf("break-even rise leaves profit %g", got)
	}
}

func TestValidationStartsFlatAndKeepsEarlierIndicatorWarmup(t *testing.T) {
	df := waveFrame(10)
	before := func(i int) (bool, bool) { return i == 1, i == 7 }
	if events := df.simulateCosts(before, TradingCosts{}, 6); events == nil || len(events.Signals) != 0 {
		t.Fatal("training position leaked into validation", events)
	}
	after := func(i int) (bool, bool) { return i == 5, i == 7 }
	events := df.simulateCosts(after, TradingCosts{}, 6)
	if events == nil || len(events.Signals) != 2 || !events.Signals[0].Time.Equal(df.Candles[6].Time) || !events.Signals[1].Time.Equal(df.Candles[8].Time) {
		t.Fatal("validation did not start on its first eligible fill", events)
	}
}

func TestFutureValidationPricesNeverTuneTrainingParameters(t *testing.T) {
	df := waveFrame(400)
	policy := evaluationPolicy()
	before, err := df.OptimizeValidated(policy)
	if err != nil {
		t.Fatal(err)
	}
	changed := df.indicatorFrame()
	changed.Candles = append([]Candle(nil), df.Candles...)
	for i := before.TrainingCount; i < len(changed.Candles); i++ {
		price := 300 + 20*math.Sin(float64(i)*.5)
		changed.Candles[i].Open = price
		changed.Candles[i].Close = price
		changed.Candles[i].High = price + 2
		changed.Candles[i].Low = price - 2
	}
	after, err := changed.OptimizeValidated(policy)
	if err != nil {
		t.Fatal(err)
	}
	sawDifferentValidation := false
	for _, a := range before.Rankings {
		for _, b := range after.Rankings {
			if a.Strategy != b.Strategy {
				continue
			}
			if !reflect.DeepEqual(a.Parameters, b.Parameters) || a.TrainingProfit != b.TrainingProfit || a.TrainingTrades != b.TrainingTrades {
				t.Fatalf("validation leaked into tuning: %s", a.Strategy)
			}
			if a.Performance != b.Performance {
				sawDifferentValidation = true
			}
		}
	}
	if !sawDifferentValidation {
		t.Fatal("fixture did not exercise validation prices")
	}
	if before.TrainingCount+before.ValidationCount != len(df.Candles) || !before.ValidationStart.Equal(df.Candles[before.TrainingCount].Time) {
		t.Fatal("incorrect chronological split")
	}
}

func TestValidationRequiresNetProfitAndEnoughCompletedTrades(t *testing.T) {
	df := waveFrame(400)
	for i := range df.Candles {
		price := 100 + .1*math.Sin(float64(i)*.13)
		df.Candles[i].Open = price
		df.Candles[i].Close = price
		df.Candles[i].High = price + .01
		df.Candles[i].Low = price - .01
	}
	policy := evaluationPolicy()
	policy.Costs = TradingCosts{}
	policy.MinValidationTrades = 1
	free, err := df.OptimizeValidated(policy)
	if err != nil {
		t.Fatal(err)
	}
	if !free.Parameters.Enabled() {
		t.Fatal("fixture must produce a gross-profitable strategy")
	}
	policy.Costs = evaluationPolicy().Costs
	net, err := df.OptimizeValidated(policy)
	if err != nil {
		t.Fatal(err)
	}
	if net.Parameters.Enabled() {
		t.Fatal("tiny gross profits survived round-trip costs")
	}
	policy = evaluationPolicy()
	policy.MinValidationTrades = 1000
	few, err := waveFrame(400).OptimizeValidated(policy)
	if err != nil {
		t.Fatal(err)
	}
	if few.Parameters.Enabled() {
		t.Fatal("insufficient validation trades were enabled")
	}
	for _, rank := range few.Rankings {
		if rank.Available && rank.Performance > rank.GrossPerformance+1e-9 {
			t.Fatal("costs increased performance", rank)
		}
	}
}

func TestResearchRejectsInvalidCostsAndMissingCandles(t *testing.T) {
	df := waveFrame(200)
	for _, costs := range []TradingCosts{{FeeRate: math.NaN()}, {SpreadRate: -1}, {SlippageRate: math.Inf(1)}, {FeeRate: .2}} {
		if got := df.RunBacktestWithCosts("ema", DefaultBacktestParams("ema"), costs); got != nil {
			t.Fatal("invalid costs accepted")
		}
	}
	policy := evaluationPolicy()
	policy.ValidationRatio = 1
	if _, err := df.OptimizeValidated(policy); err == nil {
		t.Fatal("invalid split accepted")
	}
	policy.ValidationRatio = 1e-300
	if _, err := df.OptimizeValidated(policy); err != nil {
		t.Fatal(err)
	} // Must not index beyond the last candle.
	df.Candles = append(df.Candles[:30], df.Candles[31:]...)
	if got := df.RunBacktestWithCosts("ema", DefaultBacktestParams("ema"), TradingCosts{}); got != nil {
		t.Fatal("backtest bridged a missing candle")
	}
}
