package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"gotrading/app/models"
	"gotrading/bitflyer"
	"gotrading/config"
)

type fakeTickerSource struct {
	tickers []bitflyer.Ticker
	err     error
	wait    bool
}

func (source fakeTickerSource) GetRealTimeTickerContext(ctx context.Context, _ string, output chan<- bitflyer.Ticker) error {
	defer close(output)
	for _, ticker := range source.tickers {
		select {
		case output <- ticker:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if source.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return source.err
}

func setupApplication(t *testing.T) {
	t.Helper()
	previousConfig, previousDB := config.Config, models.DbConnection
	config.Config = config.ConfigList{
		ProductCode: "BTC_USD", SQLDriver: "sqlite3", DbName: ":memory:",
		Durations:     map[string]time.Duration{"1s": time.Second, "1m": time.Minute, "1h": time.Hour},
		TradeDuration: time.Minute,
	}
	if err := models.InitDB("sqlite3", ":memory:", config.Config.ProductCode, config.Config.Durations); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		models.DbConnection.Close()
		models.DbConnection = previousDB
		config.Config = previousConfig
	})
}

func streamSample(stamp time.Time, id int, price, volume float64) bitflyer.Ticker {
	return bitflyer.Ticker{
		ProductCode: "BTC_USD", TickID: id, Timestamp: stamp.Format(time.RFC3339Nano),
		BestBid: price - 1, BestAsk: price + 1, Volume: 5000, VolumeByProduct: volume,
	}
}

func TestIngestionBuildsAllDurationsAndSkipsDuplicateOrStaleTicks(t *testing.T) {
	setupApplication(t)
	start := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	second := streamSample(start.Add(200*time.Millisecond), 2, 110, 1002)
	source := fakeTickerSource{tickers: []bitflyer.Ticker{
		streamSample(start.Add(100*time.Millisecond), 1, 100, 1000),
		second,
		second,
		streamSample(start.Add(300*time.Millisecond), 3, 90, 1005),
		streamSample(start.Add(150*time.Millisecond), 1, 1000, 2000),
		streamSample(start.Add(time.Second), 4, 105, 1004),
		streamSample(start.Add(2*time.Second), 5, 106, 1007),
	}}
	if err := ingestStream(context.Background(), source, config.Config); err != nil {
		t.Fatal(err)
	}
	for _, duration := range []time.Duration{time.Minute, time.Hour} {
		candle, err := models.GetCandle("BTC_USD", duration, start)
		if err != nil || candle.Open != 100 || candle.Close != 106 || candle.High != 110 || candle.Low != 90 || candle.Volume != 8 {
			t.Fatalf("duration=%s candle=%+v err=%v", duration, candle, err)
		}
	}
	df, err := models.GetAllCandle("BTC_USD", time.Second, 100)
	if err != nil || len(df.Candles) != 3 || df.Candles[0].Volume != 5 || df.Candles[1].Volume != 0 || df.Candles[2].Volume != 3 {
		t.Fatalf("second candles=%+v err=%v", df, err)
	}
}

func TestIngestionErrorsAndCancellation(t *testing.T) {
	setupApplication(t)
	want := errors.New("stream disconnected")
	if err := ingestStream(context.Background(), fakeTickerSource{err: want}, config.Config); !errors.Is(err, want) {
		t.Fatalf("stream error was hidden: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ingestStream(ctx, fakeTickerSource{wait: true}, config.Config); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	ticker := streamSample(time.Now().UTC(), 1, 100, 1000)
	ticker.Timestamp = "invalid"
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ingestStream(ctx, fakeTickerSource{tickers: []bitflyer.Ticker{ticker}, wait: true}, config.Config); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invalid ticker must stop ingestion immediately: %v", err)
	}
	models.DbConnection.Close()
	ticker.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := ingestStream(ctx, fakeTickerSource{tickers: []bitflyer.Ticker{ticker}, wait: true}, config.Config); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("database error must stop ingestion immediately: %v", err)
	}
}

func TestBarNotificationsFollowConfiguredDurationAndNeverBlockIngestion(t *testing.T) {
	setupApplication(t)
	start := time.Now().UTC().Truncate(time.Minute)
	source := fakeTickerSource{tickers: []bitflyer.Ticker{
		streamSample(start, 1, 100, 1000),
		streamSample(start.Add(time.Second), 2, 101, 1001),
		streamSample(start.Add(time.Minute), 3, 102, 1002),
		streamSample(start.Add(2*time.Minute), 4, 103, 1003),
		streamSample(start.Add(2*time.Minute+time.Second), 5, 104, 1004),
	}}
	bars := make(chan bitflyer.Ticker, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ingestStreamWithBars(ctx, source, config.Config, bars); err != nil {
		t.Fatal(err)
	}
	select {
	case latest := <-bars:
		if latest.TickID != 4 {
			t.Fatalf("expected first tick of newest minute, got %+v", latest)
		}
	default:
		t.Fatal("new candle did not notify the trading worker")
	}
	candle, err := models.GetCandle("BTC_USD", time.Minute, start.Add(2*time.Minute))
	if err != nil || candle.Close != 104 {
		t.Fatal("unconsumed trade notifications stopped ingestion", candle, err)
	}
}
