package models

import (
	"database/sql"
	"fmt"
	"math"
	"time"

	"gotrading/bitflyer"
)

type Candle struct {
	ProductCode string        `json:"product_code"`
	Duration    time.Duration `json:"duration"`
	Time        time.Time     `json:"time"`
	Open        float64       `json:"open"`
	Close       float64       `json:"close"`
	High        float64       `json:"high"`
	Low         float64       `json:"low"`
	Volume      float64       `json:"volume"`
}

func NewCandle(productCode string, duration time.Duration, dateTime time.Time, open, close, high, low, volume float64) *Candle {
	return &Candle{
		ProductCode: productCode, Duration: duration, Time: dateTime.UTC(),
		Open: open, Close: close, High: high, Low: low, Volume: volume,
	}
}

func (c *Candle) TableName() string {
	return GetCandleTableName(c.ProductCode, c.Duration)
}

func (c *Candle) Create() error {
	table, err := candleTable(c.ProductCode, c.Duration)
	if err != nil {
		return err
	}
	db, err := database()
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO `+table+` (time, open, close, high, low, volume) VALUES (?, ?, ?, ?, ?, ?)`,
		c.Time.UTC().Format(time.RFC3339Nano), c.Open, c.Close, c.High, c.Low, c.Volume)
	return err
}

func (c *Candle) Save() error {
	table, err := candleTable(c.ProductCode, c.Duration)
	if err != nil {
		return err
	}
	db, err := database()
	if err != nil {
		return err
	}
	result, err := db.Exec(`UPDATE `+table+` SET open = ?, close = ?, high = ?, low = ?, volume = ? WHERE time = ?`,
		c.Open, c.Close, c.High, c.Low, c.Volume, c.Time.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

func GetCandle(productCode string, duration time.Duration, dateTime time.Time) (*Candle, error) {
	table, err := candleTable(productCode, duration)
	if err != nil {
		return nil, err
	}
	db, err := database()
	if err != nil {
		return nil, err
	}
	candle := &Candle{ProductCode: productCode, Duration: duration}
	err = db.QueryRow(`SELECT time, open, close, high, low, volume FROM `+table+` WHERE time = ?`,
		dateTime.UTC().Format(time.RFC3339Nano)).Scan(&candle.Time, &candle.Open, &candle.Close, &candle.High, &candle.Low, &candle.Volume)
	if err != nil {
		return nil, err
	}
	return candle, nil
}

// volume is the nonnegative increase in the ticker's rolling volume, not its 24h total.
func CreateCandleWithDuration(ticker bitflyer.Ticker, productCode string, duration time.Duration, volume float64) (bool, error) {
	table, err := candleTable(productCode, duration)
	if err != nil {
		return false, err
	}
	dateTime, price := ticker.DateTime(), ticker.GetMidPrice()
	if ticker.ProductCode != productCode || dateTime.IsZero() || !validAmount(ticker.BestBid) || !validAmount(ticker.BestAsk) || ticker.BestBid > ticker.BestAsk || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) || volume < 0 || math.IsNaN(volume) || math.IsInf(volume, 0) {
		return false, fmt.Errorf("invalid ticker data")
	}
	db, err := database()
	if err != nil {
		return false, err
	}
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	stamp := dateTime.UTC().Truncate(duration).Format(time.RFC3339Nano)
	result, err := tx.Exec(`INSERT INTO `+table+` (time, open, close, high, low, volume)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(time) DO NOTHING`, stamp, price, price, price, price, volume)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count == 0 {
		if _, err := tx.Exec(`UPDATE `+table+` SET close = ?, high = MAX(high, ?), low = MIN(low, ?), volume = volume + ? WHERE time = ?`,
			price, price, price, volume, stamp); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return count > 0, nil
}

func GetAllCandle(productCode string, duration time.Duration, limit int) (*DataFrameCandle, error) {
	table, err := candleTable(productCode, duration)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("candle limit must be between 1 and 1000")
	}
	db, err := database()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT time, open, close, high, low, volume FROM (
		SELECT time, open, close, high, low, volume FROM `+table+` ORDER BY time DESC LIMIT ?
		) ORDER BY time ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	df := &DataFrameCandle{ProductCode: productCode, Duration: duration, Candles: make([]Candle, 0)}
	for rows.Next() {
		candle := Candle{ProductCode: productCode, Duration: duration}
		if err := rows.Scan(&candle.Time, &candle.Open, &candle.Close, &candle.High, &candle.Low, &candle.Volume); err != nil {
			return nil, err
		}
		df.Candles = append(df.Candles, candle)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return df, nil
}
