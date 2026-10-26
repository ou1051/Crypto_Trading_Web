package controllers

import (
	"context"
	"errors"
	"reflect"
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
		Durations:       map[string]time.Duration{"1s": time.Second, "1m": time.Minute, "1h": time.Hour},
		TradeDuration:   time.Minute,
		ValidationRatio: .3, MinValidationTrades: 2, DataLimit: 365, NumRanking: 3,
		FeeRate: .0012, SpreadRate: .0005, SlippageRate: .0005,
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

type scriptedTickerSource struct {
	sessions []fakeTickerSource
	next     int
}

func (source *scriptedTickerSource) GetRealTimeTickerContext(ctx context.Context, product string, out chan<- bitflyer.Ticker) error {
	if source.next >= len(source.sessions) {
		close(out)
		return errors.New("disconnected")
	}
	session := source.sessions[source.next]
	source.next++
	return session.GetRealTimeTickerContext(ctx, product, out)
}

func TestReconnectResetsVolumeBaselineAndRetainsTimestampWatermark(t *testing.T) {
	setupApplication(t)
	start := time.Now().UTC().Truncate(time.Minute)
	source := &scriptedTickerSource{sessions: []fakeTickerSource{
		{tickers: []bitflyer.Ticker{streamSample(start, 1, 100, 1000), streamSample(start.Add(time.Second), 2, 101, 1002)}, err: errors.New("disconnect")},
		{tickers: []bitflyer.Ticker{
			streamSample(start, 1, 999, 5000), // Replayed older tick must not rewrite stored OHLC.
			streamSample(start.Add(2*time.Second), 3, 102, 9000),
			streamSample(start.Add(3*time.Second), 4, 103, 9003),
		}, err: errors.New("disconnect again")},
	}}
	stop := errors.New("test complete")
	var delays []time.Duration
	err := streamWithRetry(context.Background(), source, config.Config, nil, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		if len(delays) == 2 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatal("reconnection failed", delays, err)
	}
	candle, err := models.GetCandle("BTC_USD", time.Minute, start)
	if err != nil || candle.Open != 100 || candle.High != 103 || candle.Close != 103 || candle.Volume != 5 {
		t.Fatal("reconnection corrupted candles or volume", candle, err)
	}
}

func TestReconnectBackoffIsBoundedAndStorageErrorsAreFatal(t *testing.T) {
	setupApplication(t)
	source := &scriptedTickerSource{}
	stop := errors.New("test complete")
	var delays []time.Duration
	err := streamWithRetry(context.Background(), source, config.Config, nil, func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		if len(delays) == 7 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}) {
		t.Fatal("unbounded backoff", delays, err)
	}
	models.DbConnection.Close()
	called := false
	err = streamWithRetry(context.Background(), fakeTickerSource{tickers: []bitflyer.Ticker{streamSample(time.Now().UTC(), 1, 100, 1000)}}, config.Config, nil, func(context.Context, time.Duration) error { called = true; return stop })
	if err == nil || called {
		t.Fatal("storage failure was retried", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, 30*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal("backoff did not honor cancellation", err)
	}
}

func TestIngestionWaitsForAFullCandleAfterAConnectionStarts(t *testing.T) {
	setupApplication(t)
	start := time.Now().UTC().Truncate(time.Minute)
	bars := make(chan bitflyer.Ticker, 1)
	cursor := &tickerCursor{}
	first := fakeTickerSource{tickers: []bitflyer.Ticker{streamSample(start.Add(40*time.Second), 1, 100, 1000), streamSample(start.Add(time.Minute), 2, 101, 1001)}}
	if err := ingestSession(context.Background(), first, config.Config, bars, cursor); err != nil {
		t.Fatal(err)
	}
	if len(bars) != 0 {
		t.Fatal("partially collected opening candle triggered a decision")
	}
	second := fakeTickerSource{tickers: []bitflyer.Ticker{streamSample(start.Add(90*time.Second), 3, 102, 1002), streamSample(start.Add(2*time.Minute), 4, 103, 1003), streamSample(start.Add(3*time.Minute), 5, 104, 1004)}}
	if err := ingestSession(context.Background(), second, config.Config, bars, cursor); err != nil {
		t.Fatal(err)
	}
	if len(bars) != 1 || (<-bars).TickID != 5 {
		t.Fatal("reconnection did not wait for a fully collected candle")
	}
}
