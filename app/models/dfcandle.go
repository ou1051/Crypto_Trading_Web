package models

import "time"

type DataFrameCandle struct {
	ProductCode string        `json:"product_code"`
	Duration    time.Duration `json:"duration"`
	Candles     []Candle      `json:"candles"`
}

func (df *DataFrameCandle) Times() []time.Time {
	values := make([]time.Time, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Time
	}
	return values
}

func (df *DataFrameCandle) Opens() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Open
	}
	return values
}

func (df *DataFrameCandle) Closes() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Close
	}
	return values
}

func (df *DataFrameCandle) Highs() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.High
	}
	return values
}

func (df *DataFrameCandle) Low() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Low
	}
	return values
}

func (df *DataFrameCandle) Volume() []float64 {
	values := make([]float64, len(df.Candles))
	for i, candle := range df.Candles {
		values[i] = candle.Volume
	}
	return values
}
