package controllers

import (
	"fmt"
	"math"
	"net/url"
	"strconv"

	"gotrading/app/models"
	"gotrading/config"
)

type backtestRequest struct {
	events, optimize bool
	strategy         string
	params           map[string]float64
}

func backtestNumber(query url.Values, name string, fallback, minimum, maximum float64) (float64, error) {
	if !query.Has(name) {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(query.Get(name), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= minimum || value > maximum {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func parseBacktest(query url.Values) (backtestRequest, error) {
	var request backtestRequest
	var err error
	request.events, err = indicatorEnabled(query, "events")
	if err != nil {
		return request, err
	}
	request.optimize, err = indicatorEnabled(query, "optimize")
	if err != nil {
		return request, err
	}
	if !request.events {
		return request, nil
	}
	request.strategy = query.Get("strategy")
	if request.strategy == "" {
		request.strategy = "saved"
		if config.Config.BackTest {
			request.strategy = "ema"
		}
	}
	if request.strategy == "saved" || request.strategy == "auto" {
		return request, nil
	}
	request.params = models.DefaultBacktestParams(request.strategy)
	if request.params == nil {
		return request, fmt.Errorf("strategy must be ema, bbands, ichimoku, macd, rsi, saved, or auto")
	}
	for name, fallback := range request.params {
		if name == "bbandsK" {
			request.params[name], err = backtestNumber(query, name, fallback, 0, 10)
		} else if name == "rsiBuy" || name == "rsiSell" {
			request.params[name], err = backtestNumber(query, name, fallback, 0, 100)
		} else {
			var value int
			value, err = indicatorPeriod(query, name, int(fallback))
			request.params[name] = float64(value)
		}
		if err != nil {
			return request, err
		}
	}
	if request.strategy == "ema" && request.params["emaPeriod1"] >= request.params["emaPeriod2"] {
		return request, fmt.Errorf("EMA short period must be less than its long period")
	}
	if request.strategy == "macd" && request.params["macdPeriod1"] >= request.params["macdPeriod2"] {
		return request, fmt.Errorf("MACD fast period must be less than its slow period")
	}
	if request.strategy == "rsi" && (request.params["rsiBuy"] >= request.params["rsiSell"] || request.params["rsiSell"] >= 100) {
		return request, fmt.Errorf("RSI thresholds must satisfy 0 < buy < sell < 100")
	}
	return request, nil
}

func (request backtestRequest) apply(df *models.DataFrameCandle) error {
	if request.optimize {
		limit := config.Config.NumRanking
		if limit == 0 {
			limit = 3
		}
		df.Optimization = df.OptimizeStrategies(limit)
	}
	if request.events {
		if request.strategy == "auto" {
			df.Events = models.NewSignalEvents()
			if ai := activeAI.Load(); ai != nil {
				if snapshot := ai.snapshot.Load(); snapshot != nil && snapshot.status.ProductCode == df.ProductCode {
					df.Events.Signals = append(df.Events.Signals, snapshot.events...)
				}
			}
			df.Backtest = &models.BacktestResult{Strategy: "auto", Parameters: map[string]float64{}, Available: df.Automation != nil, Execution: "recorded_fills", Profit: df.Events.Profit(), CompletedTrades: df.Events.CompletedTrades()}
		} else if request.strategy == "saved" {
			if len(df.Candles) == 0 {
				df.Events = models.NewSignalEvents()
			} else if err := df.AddEvents(df.Candles[0].Time); err != nil {
				return err
			}
			df.Backtest = &models.BacktestResult{Strategy: "saved", Parameters: map[string]float64{}, Available: true, Execution: "stored", Profit: df.Events.Profit(), CompletedTrades: df.Events.CompletedTrades()}
		} else {
			df.SetBacktest(request.strategy, request.params)
		}
	}
	return nil
}
