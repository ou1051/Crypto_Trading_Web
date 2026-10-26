package controllers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"gotrading/app/models"
	"gotrading/bitflyer"
	"gotrading/config"
)

type tickerSource interface {
	GetRealTimeTickerContext(context.Context, string, chan<- bitflyer.Ticker) error
}

func StreamIngestionData(ctx context.Context) error {
	apiClient := bitflyer.New(config.Config.ApiKey, config.Config.ApiSecret)
	ai, err := NewAI(config.Config, apiClient)
	if err != nil {
		return fmt.Errorf("initialize automation: %w", err)
	}
	activeAI.Store(ai)
	ctx, cancel := context.WithCancel(ctx)
	bars := make(chan bitflyer.Ticker, 1)
	done := make(chan struct{})
	go func() { defer close(done); ai.Run(ctx, bars) }()
	defer func() { cancel(); <-done }()
	return streamWithRetry(ctx, apiClient, config.Config, bars, waitForRetry)
}

type tickerStreamError struct{ error }

func (err *tickerStreamError) Unwrap() error { return err.error }

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Only feed failures are retried. Invalid ticks and storage errors remain fatal.
// Restarting the ingestion session resets the rolling-volume baseline and its
// partial-candle warmup, while keeping the trading worker and order journal alive.
func streamWithRetry(ctx context.Context, source tickerSource, settings config.ConfigList, bars chan bitflyer.Ticker, wait func(context.Context, time.Duration) error) error {
	delay := time.Second
	cursor := &tickerCursor{}
	for {
		started := time.Now()
		err := ingestSession(ctx, source, settings, bars, cursor)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var feedError *tickerStreamError
		if err != nil && !errors.As(err, &feedError) {
			return err
		}
		if time.Since(started) >= time.Minute {
			delay = time.Second
		}
		log.Printf("action=reconnect_ticker retry_in=%s err=%v", delay, err)
		if err := wait(ctx, delay); err != nil {
			return err
		}
		delay = min(delay*2, 30*time.Second)
	}
}

func ingestStream(ctx context.Context, source tickerSource, settings config.ConfigList) (err error) {
	return ingestStreamWithBars(ctx, source, settings, nil)
}

func ingestStreamWithBars(ctx context.Context, source tickerSource, settings config.ConfigList, bars chan bitflyer.Ticker) (err error) {
	return ingestSession(ctx, source, settings, bars, &tickerCursor{})
}

type tickerCursor struct {
	stamp time.Time
	id    int
}

func ingestSession(ctx context.Context, source tickerSource, settings config.ConfigList, bars chan bitflyer.Ticker, cursor *tickerCursor) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	tickers := make(chan bitflyer.Ticker)
	done := make(chan error, 1)
	go func() { done <- source.GetRealTimeTickerContext(ctx, settings.ProductCode, tickers) }()
	defer func() {
		cancel()
		sourceErr := <-done
		if err == nil {
			if sourceErr != nil {
				err = &tickerStreamError{sourceErr}
			}
		}
	}()

	var previous *bitflyer.Ticker
	var readyAfter time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ticker, open := <-tickers:
			if !open {
				return nil
			}
			if ticker.ProductCode != settings.ProductCode || ticker.DateTime().IsZero() {
				return fmt.Errorf("received ticker with invalid product or timestamp")
			}
			if ticker.VolumeByProduct < 0 || math.IsNaN(ticker.VolumeByProduct) || math.IsInf(ticker.VolumeByProduct, 0) {
				return fmt.Errorf("received ticker with invalid volume")
			}
			if ticker.DateTime().Before(cursor.stamp) || (ticker.DateTime().Equal(cursor.stamp) && ticker.TickID == cursor.id) {
				continue
			}
			if readyAfter.IsZero() {
				readyAfter = ticker.DateTime().Truncate(settings.TradeDuration).Add(2 * settings.TradeDuration)
			}
			volume := 0.0
			if previous != nil {
				// A rolling 24h total cannot give exact trade volume. Use only positive
				// changes as an estimate; never repeatedly add the entire 24h total.
				volume = math.Max(0, ticker.VolumeByProduct-previous.VolumeByProduct)
			}
			newTradeBar := false
			for _, duration := range settings.Durations {
				created, err := models.CreateCandleWithDuration(ticker, settings.ProductCode, duration, volume)
				if err != nil {
					return fmt.Errorf("save %s candle: %w", duration, err)
				}
				if duration == settings.TradeDuration && created {
					newTradeBar = true
				}
			}
			if newTradeBar && bars != nil && !ticker.DateTime().Truncate(settings.TradeDuration).Before(readyAfter) {
				// Slow HTTP order checks must not stop candle ingestion. Only the
				// newest unprocessed bar is useful; old signals must not be replayed.
				select {
				case <-bars:
				default:
				}
				select {
				case bars <- ticker:
				default:
				}
			}
			previous = &ticker
			cursor.stamp, cursor.id = ticker.DateTime(), ticker.TickID
		}
	}
}
