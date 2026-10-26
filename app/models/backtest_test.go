package models

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func waveFrame(count int) *DataFrameCandle {
	df := &DataFrameCandle{ProductCode: "BTC_USD", Duration: time.Minute}
	stamp := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		price := 150 + 30*math.Sin(float64(i)*.13) + 10*math.Sin(float64(i)*.037)
		df.Candles = append(df.Candles, *NewCandle(df.ProductCode, df.Duration, stamp.Add(time.Duration(i)*time.Minute), price+.7, price, price+2, price-2, 1))
	}
	return df
}

func TestBacktestsUseNextOpenAndNeverDependOnFutureCandles(t *testing.T) {
	df := waveFrame(400)
	for _, strategy := range []string{"ema", "bbands", "ichimoku", "macd", "rsi"} {
		t.Run(strategy, func(t *testing.T) {
			params := DefaultBacktestParams(strategy)
			all := df.RunBacktest(strategy, params)
			if all == nil {
				t.Fatal("strategy unavailable on sufficient data")
			}
			if strategy != "ichimoku" && all.CompletedTrades() == 0 {
				t.Fatal("fixture must exercise actual completed trades")
			}
			for i, event := range all.Signals {
				index := int(event.Time.Sub(df.Candles[0].Time) / time.Minute)
				if index < 2 || event.Price != df.Candles[index].Open || i > 0 && !all.Signals[i-1].Time.Before(event.Time) {
					t.Fatalf("incorrect next-open fill: %+v", event)
				}
			}
			prefix := df.indicatorFrame()
			prefix.Candles = df.Candles[:220]
			prior := prefix.RunBacktest(strategy, params)
			if prior == nil {
				t.Fatal("prefix unexpectedly unavailable")
			}
			visible := NewSignalEvents()
			for _, event := range all.Signals {
				if !event.Time.After(prefix.Candles[219].Time) {
					visible.Signals = append(visible.Signals, event)
				}
			}
			if !reflect.DeepEqual(prior.Signals, visible.Signals) {
				t.Fatal("future prices changed historical trade signals")
			}
			changed := df.indicatorFrame()
			changed.Candles = append([]Candle(nil), df.Candles...)
			changed.Candles[len(changed.Candles)-1].Close = 100000
			if got := changed.RunBacktest(strategy, params); !reflect.DeepEqual(all.Signals, got.Signals) {
				t.Fatal("forming candle's close changed a trade decision")
			}
		})
	}
}

func TestOptimizationRanksHonestResultsAndNeverWritesEvents(t *testing.T) {
	setupDatabase(t, ":memory:")
	df := waveFrame(300)
	result := df.OptimizeStrategies(2)
	if len(result.Rankings) != 5 {
		t.Fatal("strategy missing from ranking")
	}
	enabled := 0
	for i, rank := range result.Rankings {
		if rank.Enable {
			enabled++
			if !rank.Available || rank.Performance <= 0 {
				t.Fatal("nonprofitable strategy enabled")
			}
		}
		if rank.Available {
			events := df.RunBacktest(rank.Strategy, rank.Parameters)
			if events == nil || events.CompletedTrades() != rank.CompletedTrades || math.Abs(events.Profit()-rank.Performance) > 1e-9 {
				t.Fatal("ranking does not match returned parameters")
			}
		}
		if i > 0 && rank.Available && result.Rankings[i-1].Available && result.Rankings[i-1].Performance < rank.Performance {
			t.Fatal("ranking not sorted")
		}
	}
	if enabled > 2 {
		t.Fatal("ranking count ignored")
	}
	var saved int
	if err := DbConnection.QueryRow(`SELECT COUNT(*) FROM signal_events`).Scan(&saved); err != nil || saved != 0 {
		t.Fatal("simulation wrote persisted trades", err)
	}
	flat := waveFrame(100)
	for i := range flat.Candles {
		flat.Candles[i].Open = 100
		flat.Candles[i].Close = 100
		flat.Candles[i].High = 101
		flat.Candles[i].Low = 99
	}
	for _, rank := range flat.OptimizeStrategies(3).Rankings {
		if rank.Enable || rank.Available || rank.Performance != 0 {
			t.Fatal("flat data was advertised as a profitable strategy")
		}
	}
	for _, rank := range waveFrame(2).OptimizeStrategies(3).Rankings {
		if rank.Available || rank.Enable {
			t.Fatal("insufficient data marked available")
		}
	}
}

func TestIchimokuTradesUseOnlyCurrentlyKnownPrices(t *testing.T) {
	df := waveFrame(130)
	for i := range df.Candles {
		price := 100.0
		if i >= 52 && i <= 84 {
			price = 130 + float64(i-52)*2
		}
		if i >= 85 {
			price = 190 - float64(i-85)*4
			if price < 90 {
				price = 90
			}
		}
		df.Candles[i].Open = price + .7
		df.Candles[i].Close = price
		df.Candles[i].High = price + 2
		df.Candles[i].Low = price - 2
	}
	df.Candles[53].High = 220
	df.Candles[83].Low = 80
	df.Candles[84].Low = 120
	events := df.BackTestIchimoku()
	if events == nil || len(events.Signals) != 2 || events.Signals[0].Time != df.Candles[81].Time || events.Signals[1].Time != df.Candles[111].Time {
		t.Fatalf("fixture must exercise the causal Ichimoku buy/sell: %+v", events)
	}
	prefix := df.indicatorFrame()
	prefix.Candles = df.Candles[:100]
	before := prefix.BackTestIchimoku()
	if before == nil || len(before.Signals) != 1 || !reflect.DeepEqual(before.Signals, events.Signals[:1]) {
		t.Fatal("future closing prices influenced historical Ichimoku signals")
	}
}

func TestOptimizerReportsNegativeBestInsteadOfInventingZeroProfit(t *testing.T) {
	df := waveFrame(300)
	buys, sells := map[time.Time]bool{}, map[time.Time]bool{}
	for short := 5; short < 11; short++ {
		for long := 12; long < 20; long++ {
			for _, event := range df.BackTestEma(short, long).Signals {
				if event.Side == "BUY" {
					buys[event.Time] = true
				} else {
					sells[event.Time] = true
				}
			}
		}
	}
	for i := range df.Candles {
		stamp := df.Candles[i].Time
		if buys[stamp] && sells[stamp] {
			t.Fatal("loss-only fixture has conflicting entries")
		}
		if buys[stamp] {
			df.Candles[i].Open = 10000
		}
		if sells[stamp] {
			df.Candles[i].Open = 1
		}
	}
	rank := df.optimizeStrategy("ema")
	if !rank.Available || rank.Performance >= 0 || rank.Enable {
		t.Fatalf("loss-only optimization was misreported: %+v", rank)
	}
}
