package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

type SignalEvent struct {
	Time        time.Time `json:"time"`
	ProductCode string    `json:"product_code"`
	Side        string    `json:"side"`
	Price       float64   `json:"price"`
	Size        float64   `json:"size"`
}

func validAmount(value float64) bool { return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value) }

func (s *SignalEvent) Save() error {
	if s.Time.IsZero() || !validProductCode.MatchString(s.ProductCode) || (s.Side != "BUY" && s.Side != "SELL") || !validAmount(s.Price) || !validAmount(s.Size) {
		return fmt.Errorf("invalid signal event")
	}
	db, err := database()
	if err != nil {
		return err
	}
	stamp := s.Time.UTC().Format(time.RFC3339Nano)
	result, err := db.Exec(`INSERT INTO signal_events (time,product_code,side,price,size) VALUES (?,?,?,?,?) ON CONFLICT(time) DO NOTHING`, stamp, s.ProductCode, s.Side, s.Price, s.Size)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count > 0 {
		return err
	}
	var existing SignalEvent
	if err := db.QueryRow(`SELECT time,product_code,side,price,size FROM signal_events WHERE time=?`, stamp).Scan(&existing.Time, &existing.ProductCode, &existing.Side, &existing.Price, &existing.Size); err != nil {
		return err
	}
	if existing.ProductCode != s.ProductCode || existing.Side != s.Side || existing.Price != s.Price || existing.Size != s.Size {
		return fmt.Errorf("conflicting signal event at %s", stamp)
	}
	return nil
}

type SignalEvents struct {
	Signals []SignalEvent `json:"signals"`
}

func NewSignalEvents() *SignalEvents { return &SignalEvents{Signals: make([]SignalEvent, 0)} }

func scanSignals(rows *sql.Rows) (*SignalEvents, error) {
	defer rows.Close()
	events := NewSignalEvents()
	for rows.Next() {
		var event SignalEvent
		if err := rows.Scan(&event.Time, &event.ProductCode, &event.Side, &event.Price, &event.Size); err != nil {
			return nil, err
		}
		events.Signals = append(events.Signals, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func GetSignalEventsByCount(productCode string, limit int) (*SignalEvents, error) {
	if !validProductCode.MatchString(productCode) || limit < 1 || limit > 10000 {
		return nil, fmt.Errorf("invalid event product or limit")
	}
	db, err := database()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT time,product_code,side,price,size FROM (
		SELECT time,product_code,side,price,size FROM signal_events WHERE product_code=? ORDER BY julianday(time) DESC LIMIT ?
	) ORDER BY julianday(time) ASC`, productCode, limit)
	if err != nil {
		return nil, err
	}
	return scanSignals(rows)
}

func GetSignalEventsInRange(productCode string, start, end time.Time) (*SignalEvents, error) {
	if !validProductCode.MatchString(productCode) || start.IsZero() || end.Before(start) {
		return nil, fmt.Errorf("invalid event range")
	}
	db, err := database()
	if err != nil {
		return nil, err
	}
	var preceding SignalEvent
	err = db.QueryRow(`SELECT time,product_code,side,price,size FROM signal_events WHERE product_code=? AND julianday(time)<julianday(?) ORDER BY julianday(time) DESC LIMIT 1`, productCode, start.UTC().Format(time.RFC3339Nano)).Scan(&preceding.Time, &preceding.ProductCode, &preceding.Side, &preceding.Price, &preceding.Size)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	hasPreceding := err == nil && preceding.Side == "BUY"
	rows, err := db.Query(`SELECT time,product_code,side,price,size FROM signal_events WHERE product_code=? AND julianday(time)>=julianday(?) AND julianday(time)<=julianday(?) ORDER BY julianday(time) ASC`, productCode, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	events, err := scanSignals(rows)
	if err != nil {
		return nil, err
	}
	// Retain an opening buy before the visible window so a visible sell keeps its cost basis.
	if hasPreceding {
		events.Signals = append([]SignalEvent{preceding}, events.Signals...)
	}
	return events, nil
}

func GetSignalEventsAfterTime(productCode string, stamp time.Time) (*SignalEvents, error) {
	events, err := GetSignalEventsInRange(productCode, stamp, time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC))
	if err != nil {
		return nil, err
	}
	return events.CollectAfter(stamp), nil
}

func (s *SignalEvents) CanBuy(stamp time.Time) bool {
	return !stamp.IsZero() && (len(s.Signals) == 0 || (s.Signals[len(s.Signals)-1].Side == "SELL" && s.Signals[len(s.Signals)-1].Time.Before(stamp)))
}

func (s *SignalEvents) CanSell(stamp time.Time) bool {
	return !stamp.IsZero() && len(s.Signals) > 0 && s.Signals[len(s.Signals)-1].Side == "BUY" && s.Signals[len(s.Signals)-1].Time.Before(stamp)
}

func (s *SignalEvents) record(event SignalEvent, save bool) bool {
	if !validProductCode.MatchString(event.ProductCode) || !validAmount(event.Price) || !validAmount(event.Size) {
		return false
	}
	if event.Side == "BUY" && !s.CanBuy(event.Time) || event.Side == "SELL" && !s.CanSell(event.Time) {
		return false
	}
	if len(s.Signals) > 0 {
		last := s.Signals[len(s.Signals)-1]
		if last.ProductCode != event.ProductCode || (event.Side == "SELL" && last.Size != event.Size) {
			return false
		}
	}
	if save {
		if err := event.Save(); err != nil {
			return false
		}
	}
	event.Time = event.Time.UTC()
	s.Signals = append(s.Signals, event)
	return true
}

func (s *SignalEvents) Buy(product string, stamp time.Time, price, size float64, save bool) bool {
	return s.record(SignalEvent{Time: stamp, ProductCode: product, Side: "BUY", Price: price, Size: size}, save)
}
func (s *SignalEvents) Sell(product string, stamp time.Time, price, size float64, save bool) bool {
	return s.record(SignalEvent{Time: stamp, ProductCode: product, Side: "SELL", Price: price, Size: size}, save)
}

func (s *SignalEvents) summary() (profit float64, trades int, holding bool) {
	var buy *SignalEvent
	for i := range s.Signals {
		event := &s.Signals[i]
		if event.Side == "BUY" && buy == nil {
			buy = event
		}
		if event.Side == "SELL" && buy != nil && buy.ProductCode == event.ProductCode && buy.Size == event.Size && buy.Time.Before(event.Time) {
			profit += (event.Price - buy.Price) * event.Size
			trades++
			buy = nil
		}
	}
	return profit, trades, buy != nil
}

func (s *SignalEvents) Profit() float64      { profit, _, _ := s.summary(); return profit }
func (s *SignalEvents) CompletedTrades() int { _, trades, _ := s.summary(); return trades }

func (s SignalEvents) MarshalJSON() ([]byte, error) {
	profit, trades, holding := s.summary()
	signals := s.Signals
	if signals == nil {
		signals = make([]SignalEvent, 0)
	}
	return json.Marshal(struct {
		Signals         []SignalEvent `json:"signals"`
		Profit          float64       `json:"profit"`
		CompletedTrades int           `json:"completed_trades"`
		Holding         bool          `json:"holding"`
	}{signals, profit, trades, holding})
}

func (s *SignalEvents) CollectAfter(stamp time.Time) *SignalEvents {
	result := NewSignalEvents()
	for _, event := range s.Signals {
		if !event.Time.Before(stamp) {
			result.Signals = append(result.Signals, event)
		}
	}
	return result
}

func (df *DataFrameCandle) AddEvents(start time.Time) error {
	df.Events = NewSignalEvents()
	if len(df.Candles) == 0 {
		return nil
	}
	events, err := GetSignalEventsInRange(df.ProductCode, start, df.Candles[len(df.Candles)-1].Time)
	if err != nil {
		return err
	}
	df.Events = events
	return nil
}
