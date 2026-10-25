package controllers

import (
	"context"
	"fmt"
	"math"

	"gotrading/app/models"
	"gotrading/bitflyer"
	"gotrading/config"
)

type tickerSource interface {
	GetRealTimeTickerContext(context.Context, string, chan<- bitflyer.Ticker) error
}

func StreamIngestionData(ctx context.Context) error {
	apiClient := bitflyer.New(config.Config.ApiKey, config.Config.ApiSecret)
	return ingestStream(ctx, apiClient, config.Config)
}

func ingestStream(ctx context.Context, source tickerSource, settings config.ConfigList) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	tickers := make(chan bitflyer.Ticker)
	done := make(chan error, 1)
	go func() { done <- source.GetRealTimeTickerContext(ctx, settings.ProductCode, tickers) }()
	defer func() {
		cancel()
		sourceErr := <-done
		if err == nil {
			err = sourceErr
		}
	}()

	var previous *bitflyer.Ticker
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
			volume := 0.0
			if previous != nil {
				if ticker.DateTime().Before(previous.DateTime()) ||
					(ticker.TickID == previous.TickID && ticker.DateTime().Equal(previous.DateTime())) {
					continue
				}
				// A rolling 24h total cannot give exact trade volume. Use only positive
				// changes as an estimate; never repeatedly add the entire 24h total.
				volume = math.Max(0, ticker.VolumeByProduct-previous.VolumeByProduct)
			}
			for _, duration := range settings.Durations {
				if _, err := models.CreateCandleWithDuration(ticker, settings.ProductCode, duration, volume); err != nil {
					return fmt.Errorf("save %s candle: %w", duration, err)
				}
			}
			previous = &ticker
		}
	}
}
