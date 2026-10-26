package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignalEventsAlternateAndCalculateOnlyCompletedTrades(t *testing.T) {
	events := NewSignalEvents()
	stamp := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	if events.Sell("BTC_USD", stamp, 100, 1, false) || events.Buy("BTC_USD", stamp, 100, -1, false) {
		t.Fatal("invalid initial order accepted")
	}
	if !events.Buy("BTC_USD", stamp, 100, 1, false) || events.Buy("BTC_USD", stamp.Add(time.Second), 110, 1, false) || events.Sell("BTC_USD", stamp, 110, 1, false) || events.Sell("BTC_USD", stamp.Add(time.Second), 110, 2, false) || events.Sell("BTC_JPY", stamp.Add(time.Second), 110, 1, false) {
		t.Fatal("position validation failed")
	}
	if !events.Sell("BTC_USD", stamp.Add(time.Second), 120, 1, false) || !events.Buy("BTC_USD", stamp.Add(2*time.Second), 90, 1, false) {
		t.Fatal("valid trades rejected")
	}
	if events.Profit() != 20 || events.CompletedTrades() != 1 {
		t.Fatal("unclosed buy changed realized profit")
	}
	data, err := json.Marshal(events)
	if err != nil || !strings.Contains(string(data), `"holding":true`) {
		t.Fatalf("body=%s err=%v", data, err)
	}
	empty, err := json.Marshal(events.CollectAfter(stamp.Add(time.Hour)))
	if err != nil || !strings.Contains(string(empty), `"signals":[]`) || !strings.Contains(string(empty), `"profit":0`) {
		t.Fatalf("empty events must encode zero profit: %s", empty)
	}
}

func TestEventPersistenceScopesProductsAndRetainsOpeningCost(t *testing.T) {
	setupDatabase(t, ":memory:")
	stamp := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	buy := SignalEvent{Time: stamp, ProductCode: "BTC_USD", Side: "BUY", Price: 100, Size: 1}
	if err := buy.Save(); err != nil {
		t.Fatal(err)
	}
	if err := buy.Save(); err != nil {
		t.Fatal("identical save must be idempotent", err)
	}
	conflict := buy
	conflict.Price = 101
	if err := conflict.Save(); err == nil {
		t.Fatal("conflicting duplicate was silently accepted")
	}
	for _, event := range []SignalEvent{{Time: stamp.Add(time.Second), ProductCode: "BTC_JPY", Side: "BUY", Price: 1000, Size: 1}, {Time: stamp.Add(time.Minute), ProductCode: "BTC_USD", Side: "SELL", Price: 120, Size: 1}, {Time: stamp.Add(2 * time.Minute), ProductCode: "BTC_USD", Side: "BUY", Price: 90, Size: 1}} {
		if err := event.Save(); err != nil {
			t.Fatal(err)
		}
	}
	events, err := GetSignalEventsInRange("BTC_USD", stamp.Add(time.Minute), stamp.Add(time.Minute))
	if err != nil || len(events.Signals) != 2 || events.Profit() != 20 {
		t.Fatalf("cost basis lost: events=%+v err=%v", events, err)
	}
	last, err := GetSignalEventsByCount("BTC_USD", 2)
	if err != nil || len(last.Signals) != 2 || last.Signals[0].Side != "SELL" || last.Signals[1].Price != 90 {
		t.Fatalf("latest window is wrong: %+v err=%v", last, err)
	}
	after, err := GetSignalEventsAfterTime("BTC_USD", stamp.Add(time.Minute))
	if err != nil || len(after.Signals) != 2 || after.Signals[0].Time.Before(stamp.Add(time.Minute)) {
		t.Fatalf("after-time query returned earlier records: %+v err=%v", after, err)
	}
	for _, event := range events.Signals {
		if event.ProductCode != "BTC_USD" {
			t.Fatal("another product leaked into query")
		}
	}
	DbConnection.Close()
	if err := conflict.Save(); err == nil {
		t.Fatal("database error hidden")
	}
	if NewSignalEvents().Buy("BTC_USD", stamp.Add(time.Hour), 100, 1, true) {
		t.Fatal("failed persistence must not record a successful buy")
	}
}
