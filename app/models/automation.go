package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

var ErrAutomationConflict = errors.New("automation state changed in another process")

type PendingTrade struct {
	BarTime         time.Time `json:"bar_time"`
	CreatedAt       time.Time `json:"created_at"`
	Side            string    `json:"side"`
	Size            float64   `json:"size"`
	AcceptanceID    string    `json:"acceptance_id"`
	CancelRequested bool      `json:"cancel_requested"`
}

type AutomationState struct {
	Mode           string        `json:"mode"`
	ProductCode    string        `json:"product_code"`
	Duration       time.Duration `json:"duration"`
	LastBar        time.Time     `json:"last_bar"`
	Cash           float64       `json:"cash"`
	Quantity       float64       `json:"quantity"`
	Cost           float64       `json:"cost"`
	StopLimit      float64       `json:"stop_limit"`
	RealizedProfit float64       `json:"realized_profit"`
	Params         *TradeParams  `json:"params"`
	OptimizedAt    time.Time     `json:"optimized_at"`
	Pending        *PendingTrade `json:"pending"`
	Events         []SignalEvent `json:"events"`
	Halted         bool          `json:"halted"`
	LastError      string        `json:"last_error"`
	Version        int64         `json:"-"`
}

type AutomationStatus struct {
	Mode           string        `json:"mode"`
	FeeRate        float64       `json:"fee_rate"`
	ProductCode    string        `json:"product_code"`
	Duration       time.Duration `json:"duration"`
	LastBar        time.Time     `json:"last_bar"`
	Cash           float64       `json:"cash"`
	Quantity       float64       `json:"quantity"`
	StopLimit      float64       `json:"stop_limit"`
	RealizedProfit float64       `json:"realized_profit"`
	Params         *TradeParams  `json:"params"`
	OptimizedAt    time.Time     `json:"optimized_at"`
	Pending        *PendingTrade `json:"pending"`
	Halted         bool          `json:"halted"`
	LastError      string        `json:"last_error"`
}

func LoadAutomationState(mode, product string, duration time.Duration) (*AutomationState, error) {
	if (mode != "paper" && mode != "live") || !validProductCode.MatchString(product) || duration <= 0 {
		return nil, fmt.Errorf("invalid automation key")
	}
	db, err := database()
	if err != nil {
		return nil, err
	}
	state := &AutomationState{Mode: mode, ProductCode: product, Duration: duration, Events: make([]SignalEvent, 0)}
	var payload []byte
	err = db.QueryRow(`SELECT payload,version FROM automation_state WHERE mode=? AND product_code=? AND duration=?`, mode, product, int64(duration)).Scan(&payload, &state.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(payload, state); err != nil {
		return nil, fmt.Errorf("decode automation state: %w", err)
	}
	if state.Mode != mode || state.ProductCode != product || state.Duration != duration {
		return nil, fmt.Errorf("automation state key mismatch")
	}
	if err := validateAutomationState(state); err != nil {
		return nil, err
	}
	return state, nil
}

func validateAutomationState(state *AutomationState) error {
	if state == nil || (state.Mode != "paper" && state.Mode != "live") ||
		!validProductCode.MatchString(state.ProductCode) || state.Duration <= 0 || state.LastBar.IsZero() {
		return fmt.Errorf("invalid automation state")
	}
	for _, value := range []float64{state.Cash, state.Quantity, state.Cost, state.StopLimit} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("invalid automation balance")
		}
	}
	if math.IsNaN(state.RealizedProfit) || math.IsInf(state.RealizedProfit, 0) {
		return fmt.Errorf("invalid automation profit")
	}
	if state.Quantity == 0 && state.Cost != 0 {
		return fmt.Errorf("invalid automation cost basis")
	}
	if p := state.Pending; p != nil {
		if (p.Side != "BUY" && p.Side != "SELL") || !validAmount(p.Size) || p.BarTime.IsZero() || p.CreatedAt.IsZero() {
			return fmt.Errorf("invalid pending automation trade")
		}
	}
	return nil
}

// The version check claims each bar/order before any external order is submitted.
func SaveAutomationState(state *AutomationState) error {
	if err := validateAutomationState(state); err != nil {
		return err
	}
	db, err := database()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	var result sql.Result
	if state.Version == 0 {
		result, err = db.Exec(`INSERT INTO automation_state (mode,product_code,duration,payload,version) VALUES (?,?,?,?,1) ON CONFLICT DO NOTHING`, state.Mode, state.ProductCode, int64(state.Duration), payload)
	} else {
		result, err = db.Exec(`UPDATE automation_state SET payload=?,version=version+1 WHERE mode=? AND product_code=? AND duration=? AND version=?`, payload, state.Mode, state.ProductCode, int64(state.Duration), state.Version)
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrAutomationConflict
	}
	state.Version++
	return nil
}
