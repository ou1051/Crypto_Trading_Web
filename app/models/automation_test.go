package models

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestAutomationJournalPersistsAndClaimsVersions(t *testing.T) {
	setupDatabase(t, ":memory:")
	state, err := LoadAutomationState("paper", "BTC_USD", time.Minute)
	if err != nil || state.Version != 0 || len(state.Events) != 0 {
		t.Fatal(state, err)
	}
	state.LastBar = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	state.Cash, state.Quantity, state.Cost = 50000, 1.98, 20000
	state.Pending = &PendingTrade{Side: "SELL", Size: 1, BarTime: state.LastBar, CreatedAt: state.LastBar, AcceptanceID: "JRF-test"}
	state.Events = []SignalEvent{{ProductCode: "BTC_USD", Time: state.LastBar.Add(-time.Minute), Side: "BUY", Size: 1.98, Price: 20000 / 1.98, ExecutionPrice: 10000, Commission: .02}}
	if err := SaveAutomationState(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadAutomationState("paper", "BTC_USD", time.Minute)
	if err != nil || !reflect.DeepEqual(loaded, state) {
		t.Fatalf("journal lost state: %+v %v", loaded, err)
	}
	if err := SaveAutomationState(loaded); err != nil {
		t.Fatal(err)
	}
	if err := SaveAutomationState(state); !errors.Is(err, ErrAutomationConflict) {
		t.Fatalf("stale writer was accepted: %v", err)
	}
	for _, key := range []struct {
		mode, product string
		duration      time.Duration
	}{{"live", "BTC_USD", time.Minute}, {"paper", "BTC_JPY", time.Minute}, {"paper", "BTC_USD", time.Hour}} {
		other, err := LoadAutomationState(key.mode, key.product, key.duration)
		if err != nil || other.Version != 0 || other.Quantity != 0 {
			t.Fatal("state isolation failed", other, err)
		}
	}
}

func TestAutomationJournalRejectsCorruptState(t *testing.T) {
	setupDatabase(t, ":memory:")
	stamp := time.Now().UTC()
	state := &AutomationState{Mode: "live", ProductCode: "BTC_USD", Duration: time.Minute, LastBar: stamp}
	for _, corrupt := range []func(*AutomationState){
		func(s *AutomationState) { s.Quantity = -1 }, func(s *AutomationState) { s.Cash = math.Inf(1) },
		func(s *AutomationState) { s.RealizedProfit = math.NaN() }, func(s *AutomationState) { s.Cost = 1 },
		func(s *AutomationState) {
			s.Pending = &PendingTrade{Side: "OTHER", Size: 1, BarTime: stamp, CreatedAt: stamp}
		},
	} {
		invalid := *state
		corrupt(&invalid)
		if err := SaveAutomationState(&invalid); err == nil {
			t.Fatal("invalid state was persisted", invalid)
		}
	}
	if err := SaveAutomationState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := DbConnection.Exec(`UPDATE automation_state SET payload='{"mode":"live","product_code":"BTC_USD","duration":60000000000,"quantity":-1}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAutomationState("live", "BTC_USD", time.Minute); err == nil {
		t.Fatal("corrupt journal was accepted")
	}
}

func TestSignalProfitRetainsCostBasisAcrossPartialFillsAndDust(t *testing.T) {
	stamp := time.Now().UTC()
	events := &SignalEvents{Signals: []SignalEvent{
		{Time: stamp, ProductCode: "BTC_USD", Side: "BUY", Price: 100, Size: 2},
		{Time: stamp.Add(time.Minute), ProductCode: "BTC_JPY", Side: "SELL", Price: 900, Size: 1},
		{Time: stamp.Add(2 * time.Minute), ProductCode: "BTC_USD", Side: "SELL", Price: 120, Size: .5},
		{Time: stamp.Add(3 * time.Minute), ProductCode: "BTC_USD", Side: "BUY", Price: 80, Size: .5},
		{Time: stamp.Add(4 * time.Minute), ProductCode: "BTC_USD", Side: "SELL", Price: 110, Size: 1.99999999},
	}}
	// First sale earns 10; the remaining average cost is (150+40)/2=95.
	if got := events.Profit(); math.Abs(got-(10+15*1.99999999)) > 1e-9 {
		t.Fatalf("partial-fill cost basis was lost: %f", got)
	}
}

func TestLiveDecisionsMatchBacktestSignalsAtEachCompletedCandle(t *testing.T) {
	df := waveFrame(300)
	for _, strategy := range []string{"ema", "bbands", "ichimoku", "macd", "rsi"} {
		t.Run(strategy, func(t *testing.T) {
			params := &TradeParams{}
			switch strategy {
			case "ema":
				params.EmaEnable = true
				params.EmaPeriod1 = 7
				params.EmaPeriod2 = 14
			case "bbands":
				params.BbEnable = true
				params.BbN = 20
				params.BbK = 2
			case "ichimoku":
				params.IchimokuEnable = true
			case "macd":
				params.MacdEnable = true
				params.MacdFastPeriod = 12
				params.MacdSlowPeriod = 26
				params.MacdSignalPeriod = 9
			case "rsi":
				params.RsiEnable = true
				params.RsiPeriod = 14
				params.RsiBuyThread = 30
				params.RsiSellThread = 70
			}
			live := NewSignalEvents()
			for count := 2; count < len(df.Candles); count++ {
				completed := df.indicatorFrame()
				completed.Candles = df.Candles[:count]
				decision := completed.LatestTradeDecision(params)
				fill := df.Candles[count]
				if decision.BuyVotes > 0 {
					live.Buy(df.ProductCode, fill.Time, fill.Open, 1, false)
				} else if decision.SellVotes > 0 {
					live.Sell(df.ProductCode, fill.Time, fill.Open, 1, false)
				}
			}
			backtest := df.RunBacktest(strategy, DefaultBacktestParams(strategy))
			if !reflect.DeepEqual(live.Signals, backtest.Signals) {
				t.Fatal("automation and backtest disagree on the same completed candles")
			}
		})
	}
}
