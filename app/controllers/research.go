package controllers

import (
	"gotrading/app/models"
	"gotrading/config"
)

func strategyPolicy(settings config.ConfigList) models.StrategyPolicy {
	return models.StrategyPolicy{
		Costs:           models.TradingCosts{FeeRate: settings.FeeRate, SpreadRate: settings.SpreadRate, SlippageRate: settings.SlippageRate},
		ValidationRatio: settings.ValidationRatio, MinValidationTrades: settings.MinValidationTrades,
		DataLimit: settings.DataLimit, NumRanking: settings.NumRanking,
	}
}
