package controllers

import (
	"fmt"
	"math"
	"net/url"
	"strconv"

	"gotrading/app/models"
)

type indicatorPlan []func(*models.DataFrameCandle)

func indicatorEnabled(query url.Values, name string) (bool, error) {
	if !query.Has(name) {
		return false, nil
	}
	if query.Get(name) == "" {
		return true, nil
	}
	enabled, err := strconv.ParseBool(query.Get(name))
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", name)
	}
	return enabled, nil
}

func indicatorPeriod(query url.Values, name string, fallback int) (int, error) {
	if !query.Has(name) {
		return fallback, nil
	}
	period, err := strconv.Atoi(query.Get(name))
	if err != nil || period < 2 || period > 1000 {
		return 0, fmt.Errorf("%s must be an integer between 2 and 1000", name)
	}
	return period, nil
}

func parseIndicators(query url.Values) (indicatorPlan, error) {
	plan := make(indicatorPlan, 0)
	for _, group := range []struct {
		name     string
		defaults [3]int
		add      func(*models.DataFrameCandle, int) bool
	}{
		{"sma", [3]int{7, 14, 50}, (*models.DataFrameCandle).AddSma},
		{"ema", [3]int{7, 14, 50}, (*models.DataFrameCandle).AddEma},
		{"hv", [3]int{21, 63, 252}, (*models.DataFrameCandle).AddHv},
	} {
		enabled, err := indicatorEnabled(query, group.name)
		if err != nil {
			return nil, err
		}
		if !enabled {
			continue
		}
		for i, fallback := range group.defaults {
			period, err := indicatorPeriod(query, fmt.Sprintf("%sPeriod%d", group.name, i+1), fallback)
			if err != nil {
				return nil, err
			}
			plan = append(plan, func(df *models.DataFrameCandle) { group.add(df, period) })
		}
	}
	enabled, err := indicatorEnabled(query, "bbands")
	if err != nil {
		return nil, err
	}
	if enabled {
		n, err := indicatorPeriod(query, "bbandsN", 20)
		if err != nil {
			return nil, err
		}
		k := 2.0
		if query.Has("bbandsK") {
			k, err = strconv.ParseFloat(query.Get("bbandsK"), 64)
			if err != nil || math.IsNaN(k) || math.IsInf(k, 0) || k <= 0 || k > 10 {
				return nil, fmt.Errorf("bbandsK must be greater than 0 and at most 10")
			}
		}
		plan = append(plan, func(df *models.DataFrameCandle) { df.AddBBands(n, k) })
	}
	enabled, err = indicatorEnabled(query, "ichimoku")
	if err != nil {
		return nil, err
	}
	if enabled {
		plan = append(plan, func(df *models.DataFrameCandle) { df.AddIchimoku() })
	}
	enabled, err = indicatorEnabled(query, "rsi")
	if err != nil {
		return nil, err
	}
	if enabled {
		period, err := indicatorPeriod(query, "rsiPeriod", 14)
		if err != nil {
			return nil, err
		}
		plan = append(plan, func(df *models.DataFrameCandle) { df.AddRsi(period) })
	}
	enabled, err = indicatorEnabled(query, "macd")
	if err != nil {
		return nil, err
	}
	if enabled {
		fast, err := indicatorPeriod(query, "macdPeriod1", 12)
		if err != nil {
			return nil, err
		}
		slow, err := indicatorPeriod(query, "macdPeriod2", 26)
		if err != nil {
			return nil, err
		}
		signal, err := indicatorPeriod(query, "macdPeriod3", 9)
		if err != nil {
			return nil, err
		}
		if fast >= slow {
			return nil, fmt.Errorf("MACD fast period must be less than its slow period")
		}
		plan = append(plan, func(df *models.DataFrameCandle) { df.AddMacd(fast, slow, signal) })
	}
	return plan, nil
}
