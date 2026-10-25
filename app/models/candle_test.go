package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gotrading/bitflyer"
)

func setupDatabase(t *testing.T, filename string) {
	t.Helper()
	previous := DbConnection
	if err := InitDB("sqlite3", filename, "BTC_USD", map[string]time.Duration{
		"1s": time.Second, "1m": time.Minute, "1h": time.Hour,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		DbConnection.Close()
		DbConnection = previous
	})
}

func quoteTicker(stamp time.Time, price float64) bitflyer.Ticker {
	return bitflyer.Ticker{ProductCode: "BTC_USD", Timestamp: stamp.Format(time.RFC3339Nano), BestBid: price - 1, BestAsk: price + 1}
}

func TestCandleAggregationAndNewBucket(t *testing.T) {
	setupDatabase(t, ":memory:")
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	for i, sample := range []struct{ price, volume float64 }{{100, 0}, {105, 2}, {95, 3}, {102, 1}} {
		created, err := CreateCandleWithDuration(quoteTicker(start.Add(time.Duration(i)*time.Second), sample.price), "BTC_USD", time.Minute, sample.volume)
		if err != nil || created != (i == 0) {
			t.Fatalf("tick %d: created=%v err=%v", i, created, err)
		}
	}
	created, err := CreateCandleWithDuration(quoteTicker(start.Add(time.Minute), 110), "BTC_USD", time.Minute, 5)
	if err != nil || !created {
		t.Fatalf("new minute: created=%v err=%v", created, err)
	}
	candle, err := GetCandle("BTC_USD", time.Minute, start)
	if err != nil {
		t.Fatal(err)
	}
	if candle.Open != 100 || candle.Close != 102 || candle.High != 105 || candle.Low != 95 || candle.Volume != 6 {
		t.Fatalf("incorrect OHLCV: %+v", candle)
	}
	if _, err := GetCandle("BTC_USD", time.Minute, start.Add(time.Hour)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing candle must return sql.ErrNoRows, got %v", err)
	}
}

func TestCandlePersistenceAndSave(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "candles.sqlite")
	setupDatabase(t, filename)
	stamp := time.Date(2026, 10, 25, 10, 0, 0, 0, time.FixedZone("UTC+9", 9*60*60))
	candle := NewCandle("BTC_USD", time.Second, stamp, 100, 105, 110, 90, 3)
	if err := candle.Create(); err != nil {
		t.Fatal(err)
	}
	candle.Close, candle.Volume = 108, 5
	if err := candle.Save(); err != nil {
		t.Fatal(err)
	}
	if err := DbConnection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB("sqlite3", filename, "BTC_USD", map[string]time.Duration{"1s": time.Second}); err != nil {
		t.Fatal(err)
	}
	loaded, err := GetCandle("BTC_USD", time.Second, stamp.UTC())
	if err != nil || !loaded.Time.Equal(stamp) || loaded.Close != 108 || loaded.Volume != 5 {
		t.Fatalf("persisted candle=%+v err=%v", loaded, err)
	}
}

func TestLatestCandlesAreReturnedChronologically(t *testing.T) {
	setupDatabase(t, ":memory:")
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	for _, minute := range []int{3, 1, 2} {
		price := float64(minute * 100)
		if err := NewCandle("BTC_USD", time.Minute, start.Add(time.Duration(minute)*time.Minute), price, price+1, price+2, price-1, price+3).Create(); err != nil {
			t.Fatal(err)
		}
	}
	df, err := GetAllCandle("BTC_USD", time.Minute, 2)
	if err != nil || len(df.Candles) != 2 {
		t.Fatalf("dataframe=%+v err=%v", df, err)
	}
	if !df.Times()[0].Equal(start.Add(2*time.Minute)) || !df.Times()[1].Equal(start.Add(3*time.Minute)) ||
		df.Opens()[0] != 200 || df.Closes()[0] != 201 || df.Highs()[0] != 202 || df.Low()[0] != 199 || df.Volume()[0] != 203 {
		t.Fatalf("incorrect latest window or dataframe columns: %+v", df)
	}
	empty, err := GetAllCandle("BTC_USD", time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(empty)
	if err != nil || !strings.Contains(string(body), `"candles":[]`) {
		t.Fatalf("empty candles must be [], body=%s err=%v", body, err)
	}
}

func TestConcurrentAggregationDoesNotLoseUpdates(t *testing.T) {
	setupDatabase(t, ":memory:")
	stamp := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	var workers sync.WaitGroup
	results := make(chan error, 21)
	for price := 90; price <= 110; price++ {
		workers.Add(1)
		go func(price float64) {
			defer workers.Done()
			_, err := CreateCandleWithDuration(quoteTicker(stamp, price), "BTC_USD", time.Minute, 1)
			results <- err
		}(float64(price))
	}
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	candle, err := GetCandle("BTC_USD", time.Minute, stamp)
	if err != nil || candle.High != 110 || candle.Low != 90 || candle.Volume != 21 {
		t.Fatalf("lost concurrent update: candle=%+v err=%v", candle, err)
	}
}

func TestDatabaseErrorsAndUnsafeParameters(t *testing.T) {
	setupDatabase(t, ":memory:")
	for _, product := range []string{`BTC_USD"; DROP TABLE signal_events; --`, "", "BTC/USD"} {
		if _, err := GetAllCandle(product, time.Minute, 100); err == nil {
			t.Errorf("accepted invalid product %q", product)
		}
	}
	if _, err := GetAllCandle("BTC_USD", 0, 100); err == nil {
		t.Error("accepted zero duration")
	}
	for _, limit := range []int{-1, 0, 1001} {
		if _, err := GetAllCandle("BTC_USD", time.Minute, limit); err == nil {
			t.Errorf("accepted limit %d", limit)
		}
	}
	// SQLite permits text in a REAL column. Scan failures must be returned to callers.
	if _, err := DbConnection.Exec(`INSERT INTO "BTC_USD_1s" (time, open, close, high, low, volume) VALUES (?, 'bad', 1, 1, 1, 0)`, "2026-10-25T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if df, err := GetAllCandle("BTC_USD", time.Second, 100); err == nil || df != nil {
		t.Fatalf("scan failure was hidden: dataframe=%+v err=%v", df, err)
	}
	DbConnection.Close()
	if _, err := CreateCandleWithDuration(quoteTicker(time.Now().UTC(), 100), "BTC_USD", time.Minute, 0); err == nil {
		t.Error("database write error was hidden")
	}
}
