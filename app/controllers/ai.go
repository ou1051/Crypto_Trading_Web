package controllers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"gotrading/app/models"
	"gotrading/bitflyer"
	"gotrading/config"
)

const minimumBTC = .001

type tradeBroker interface {
	GetBalanceContext(context.Context) ([]bitflyer.Balance, error)
	GetTickerContext(context.Context, string) (*bitflyer.Ticker, error)
	SendOrderContext(context.Context, *bitflyer.Order) (*bitflyer.ResponseSendChildOrder, error)
	ListOrderContext(context.Context, map[string]string) ([]bitflyer.Order, error)
	CancelOrderContext(context.Context, string, string) error
}

type automationSnapshot struct {
	status models.AutomationStatus
	events []models.SignalEvent
}

// AI selects technical strategies using historical performance. It is a rule-based
// model, as in the course; it does not train a machine-learning model.
// Run owns all mutations. HTTP handlers only read immutable published snapshots.
type AI struct {
	broker        tradeBroker
	settings      config.ConfigList
	state         *models.AutomationState
	now           func() time.Time
	storageFailed bool
	snapshot      atomic.Pointer[automationSnapshot]
}

var activeAI atomic.Pointer[AI]

func NewAI(settings config.ConfigList, broker tradeBroker) (*AI, error) {
	mode := "paper"
	if !settings.BackTest {
		mode = "live"
		if placeholder(settings.ApiKey) || placeholder(settings.ApiSecret) {
			return nil, fmt.Errorf("live automation requires an API key and secret")
		}
	}
	if settings.ProductCode != "BTC_JPY" && settings.ProductCode != "BTC_USD" && settings.ProductCode != "BTC_EUR" {
		return nil, fmt.Errorf("automation supports BTC spot pairs only")
	}
	policy := strategyPolicy(settings)
	if broker == nil || !policy.Valid() || settings.TradeDuration <= 0 || settings.DataLimit < 2 || settings.DataLimit > 1000 ||
		!(settings.UsePercent > 0 && settings.UsePercent <= 1) || !(settings.StopLimitPercent > 0 && settings.StopLimitPercent <= 1) ||
		!(settings.FeeRate >= 0 && settings.FeeRate <= .1) || !(settings.PaperBalance > 0 && settings.PaperBalance <= 1e15) ||
		settings.NumRanking < 1 || settings.NumRanking > 5 {
		return nil, fmt.Errorf("invalid automation settings")
	}
	state, err := models.LoadAutomationState(mode, settings.ProductCode, settings.TradeDuration)
	if err != nil {
		return nil, err
	}
	if state.Version == 0 && mode == "paper" {
		state.Cash = settings.PaperBalance
	}
	if state.Policy == nil || *state.Policy != policy {
		// Existing journals keep positions and pending orders, but parameters chosen
		// under different costs or validation rules must be selected again.
		state.Params = nil
		state.OptimizedAt = time.Time{}
	}
	state.Policy = &policy
	ai := &AI{broker: broker, settings: settings, state: state, now: time.Now}
	// Never replay a historical signal on startup, even if the process was offline.
	bar := ai.now().UTC().Truncate(settings.TradeDuration)
	if state.LastBar.Before(bar) {
		state.LastBar = bar
	}
	if state.Pending != nil && mode == "paper" {
		// No external side effect exists in paper mode. An interrupted simulation
		// was not committed, so skip that bar and continue on the next one.
		state.Pending = nil
	}
	if state.Pending != nil && state.Pending.AcceptanceID == "" {
		state.Halted = true
		state.LastError = "Order submission outcome is unknown. Reconcile the account before resuming."
	}
	if err := ai.persist(); err != nil {
		return nil, err
	}
	return ai, nil
}

func placeholder(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.Trim(value, "Xx") == ""
}

func (ai *AI) publish() {
	s := ai.state
	status := models.AutomationStatus{
		Mode: s.Mode, ProductCode: s.ProductCode, Duration: s.Duration, LastBar: s.LastBar,
		FeeRate: ai.settings.FeeRate,
		Cash:    s.Cash, Quantity: s.Quantity, StopLimit: s.StopLimit, RealizedProfit: s.RealizedProfit,
		OptimizedAt: s.OptimizedAt, Halted: s.Halted, LastError: s.LastError,
	}
	if s.Params != nil {
		params := *s.Params
		status.Params = &params
	}
	if s.Policy != nil {
		policy := *s.Policy
		status.Policy = &policy
	}
	if s.Pending != nil {
		pending := *s.Pending
		status.Pending = &pending
	}
	ai.snapshot.Store(&automationSnapshot{status: status, events: append([]models.SignalEvent{}, s.Events...)})
}

func (ai *AI) persist() error {
	if ai.storageFailed {
		return fmt.Errorf("automation journal is unavailable; restart after resolving the storage error")
	}
	err := models.SaveAutomationState(ai.state)
	if err != nil {
		// No external action may follow a failed journal write or a lost version claim.
		ai.state.Halted = true
		ai.storageFailed = true
		ai.state.LastError = "Failed to save automation state: " + err.Error()
	}
	ai.publish()
	return err
}

func (ai *AI) halt(err error) error {
	ai.state.Halted = true
	ai.state.LastError = err.Error()
	if saveErr := ai.persist(); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	return err
}

func (ai *AI) Run(ctx context.Context, bars <-chan bitflyer.Ticker) {
	poll := time.NewTicker(3 * time.Second)
	defer poll.Stop()
	defer func() {
		// Best effort only: an accepted order remains in the journal until a terminal
		// response is observed, including across shutdowns and restarts.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ai.cancelPending(cleanup); err != nil {
			log.Printf("action=automation_shutdown err=%s", err)
		}
	}()
	for {
		var err error
		select {
		case <-ctx.Done():
			return
		case ticker, ok := <-bars:
			if !ok {
				return
			}
			err = ai.onBar(ctx, ticker)
		case <-poll.C:
			err = ai.checkOrder(ctx)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("action=automation err=%s", err)
		}
	}
}

func (ai *AI) onBar(ctx context.Context, ticker bitflyer.Ticker) error {
	s := ai.state
	bar := ticker.DateTime().UTC().Truncate(s.Duration)
	if ctx.Err() != nil || s.Halted || s.Pending != nil || ticker.ProductCode != s.ProductCode || !bar.After(s.LastBar) {
		return nil
	}
	// A queued or delayed tick must not cause a trade using an old market quote.
	if !bar.Equal(ai.now().UTC().Truncate(s.Duration)) {
		return nil
	}
	s.LastBar = bar
	if err := ai.persist(); err != nil {
		return err
	}
	df, err := models.GetAllCandle(s.ProductCode, s.Duration, min(ai.settings.DataLimit+1, 1000))
	if err != nil {
		return ai.halt(err)
	}
	completed := df.Candles[:0]
	for _, candle := range df.Candles {
		if candle.Time.Before(bar) {
			completed = append(completed, candle)
		}
	}
	df.Candles = completed
	if len(completed) == 0 || !completed[len(completed)-1].Time.Equal(bar.Add(-s.Duration)) {
		return nil // Do not trade a stale signal when market data has a gap.
	}
	// Indicators assume evenly spaced bars. Rebuild their warmup after any gap.
	for i := len(df.Candles) - 1; i > 0; i-- {
		if df.Candles[i].Time.Sub(df.Candles[i-1].Time) != s.Duration {
			df.Candles = df.Candles[i:]
			break
		}
	}
	if !s.Params.Enabled() {
		selection, err := df.OptimizeValidated(*s.Policy)
		if err != nil {
			return ai.halt(err)
		}
		s.Params = selection.Parameters
		s.OptimizedAt = bar
		if err := ai.persist(); err != nil {
			return err
		}
	}
	decision := df.LatestTradeDecision(s.Params)
	if !bar.Equal(ai.now().UTC().Truncate(s.Duration)) {
		return nil
	}
	return ai.executeDecision(ctx, ticker, decision, completed[len(completed)-1].Close)
}

func positive(value float64) bool    { return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) }
func btcFloor(value float64) float64 { return math.Floor(value*1e8) / 1e8 }

func (ai *AI) sellablePosition() bool {
	return btcFloor(ai.state.Quantity/(1+ai.settings.FeeRate)) >= minimumBTC
}

func (ai *AI) executeDecision(ctx context.Context, ticker bitflyer.Ticker, decision models.TradeDecision, closePrice float64) error {
	s := ai.state
	if ctx.Err() != nil || s.Halted || s.Pending != nil {
		return nil
	}
	if ticker.DateTime().IsZero() || math.Abs(ai.now().Sub(ticker.DateTime()).Seconds()) > 30 {
		return ai.noteError(fmt.Errorf("stale decision quote"))
	}
	side := ""
	if ai.sellablePosition() {
		if decision.SellVotes > 0 || (s.StopLimit > 0 && closePrice <= s.StopLimit) {
			side = "SELL"
		}
	} else if decision.BuyVotes > 0 && decision.SellVotes == 0 {
		side = "BUY"
	}
	if side == "" {
		return nil
	}
	quote := ticker
	cash, available := s.Cash, s.Quantity
	if s.Mode == "live" {
		balances, err := ai.broker.GetBalanceContext(ctx)
		if err != nil {
			return ai.noteError(err)
		}
		cash, available = 0, 0
		currency := strings.TrimPrefix(s.ProductCode, "BTC_")
		for _, balance := range balances {
			if balance.CurrencyCode == currency {
				cash = balance.Available
			}
			if balance.CurrencyCode == "BTC" {
				available = balance.Available
			}
		}
		fresh, err := ai.broker.GetTickerContext(ctx, s.ProductCode)
		if err != nil {
			return ai.noteError(err)
		}
		if fresh == nil || fresh.ProductCode != s.ProductCode || fresh.DateTime().IsZero() || math.Abs(ai.now().Sub(fresh.DateTime()).Seconds()) > 30 {
			return ai.noteError(fmt.Errorf("missing or stale order quote"))
		}
		quote = *fresh
	}
	price := quote.BestAsk
	if s.Mode == "paper" {
		price *= 1 + ai.settings.SlippageRate
	}
	size := 0.0
	if side == "BUY" {
		if !positive(price) || !positive(cash) {
			return ai.noteError(fmt.Errorf("invalid buy quote or available balance"))
		}
		size = btcFloor(cash * ai.settings.UsePercent / price)
	} else {
		price = quote.BestBid
		if s.Mode == "paper" {
			price *= 1 - ai.settings.SlippageRate
		}
		if !positive(price) || !positive(available) {
			return ai.noteError(fmt.Errorf("invalid sell quote or available balance"))
		}
		// Sell only the position acquired by this engine, leaving a base-currency fee reserve.
		size = btcFloor(math.Min(s.Quantity, available) / (1 + ai.settings.FeeRate))
	}
	if !positive(size) || size < minimumBTC {
		return ai.noteError(fmt.Errorf("order quantity is below the 0.001 BTC minimum"))
	}
	if !ticker.DateTime().UTC().Truncate(s.Duration).Equal(ai.now().UTC().Truncate(s.Duration)) {
		return ai.noteError(fmt.Errorf("market data became stale while preparing the order"))
	}
	s.Pending = &models.PendingTrade{BarTime: s.LastBar, CreatedAt: ai.now().UTC(), Side: side, Size: size}
	s.LastError = ""
	if err := ai.persist(); err != nil {
		return err
	}
	if s.Mode == "paper" {
		return ai.completeOrder(size, price, size*ai.settings.FeeRate)
	}
	response, err := ai.broker.SendOrderContext(ctx, &bitflyer.Order{ProductCode: s.ProductCode, ChildOrderType: "MARKET", Side: side, Size: size, MinuteToExpires: 1, TimeInForce: "GTC"})
	if err != nil || response == nil || response.ChildOrderAcceptanceID == "" {
		// An HTTP error/timeout is not proof that the exchange did not accept the order.
		return ai.halt(fmt.Errorf("order submission outcome is unknown; reconcile before resuming (response error: %v)", err))
	}
	s.Pending.AcceptanceID = response.ChildOrderAcceptanceID
	if err := ai.persist(); err != nil {
		return err
	}
	return ai.checkOrder(ctx)
}

func (ai *AI) noteError(err error) error {
	ai.state.LastError = err.Error()
	if saveErr := ai.persist(); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	return err
}

func (ai *AI) cancelPending(ctx context.Context) error {
	p := ai.state.Pending
	if ai.storageFailed || p == nil || p.AcceptanceID == "" || p.CancelRequested {
		return nil
	}
	p.CancelRequested = true
	if err := ai.persist(); err != nil {
		return err
	}
	return ai.broker.CancelOrderContext(ctx, ai.state.ProductCode, p.AcceptanceID)
}

func (ai *AI) checkOrder(ctx context.Context) error {
	s := ai.state
	p := s.Pending
	if ai.storageFailed || p == nil || p.AcceptanceID == "" || ctx.Err() != nil {
		return nil
	}
	orders, err := ai.broker.ListOrderContext(ctx, map[string]string{"product_code": s.ProductCode, "child_order_acceptance_id": p.AcceptanceID})
	if err == nil {
		for _, order := range orders {
			if order.ChildOrderAcceptanceID != p.AcceptanceID {
				continue
			}
			if order.ProductCode != s.ProductCode || order.Side != p.Side {
				return ai.halt(fmt.Errorf("order response does not match the pending trade"))
			}
			switch order.ChildOrderState {
			case "COMPLETED", "CANCELED", "EXPIRED", "REJECTED":
				return ai.completeOrder(order.ExecutedSize, order.AveragePrice, order.TotalCommission)
			}
		}
	}
	if ai.now().Sub(p.CreatedAt) >= 80*time.Second && !p.CancelRequested {
		if cancelErr := ai.cancelPending(ctx); cancelErr != nil {
			return ai.noteError(cancelErr)
		}
	}
	// No executions can mean a canceled order is omitted from getchildorders.
	// Keep its journal entry; never interpret an empty response as permission to resend.
	if ai.now().Sub(p.CreatedAt) >= 3*time.Minute {
		return ai.halt(fmt.Errorf("pending order could not be reconciled; verify its executions on bitFlyer"))
	}
	if err != nil {
		return ai.noteError(err)
	}
	return nil
}

func (ai *AI) completeOrder(executed, price, commission float64) error {
	s := ai.state
	p := s.Pending
	if p == nil {
		return fmt.Errorf("no pending trade")
	}
	if math.IsNaN(executed) || math.IsInf(executed, 0) || executed < 0 || executed > p.Size+1e-8 ||
		math.IsNaN(commission) || math.IsInf(commission, 0) || commission < 0 || (executed == 0 && commission != 0) {
		return ai.halt(fmt.Errorf("invalid executed quantity or commission"))
	}
	if executed > 0 {
		if !positive(price) {
			return ai.halt(fmt.Errorf("invalid execution price"))
		}
		quantity, value := executed-commission, executed*price
		if p.Side == "SELL" {
			quantity = executed + commission
		}
		if !positive(quantity) || !positive(value) {
			return ai.halt(fmt.Errorf("invalid net execution"))
		}
		if p.Side == "BUY" {
			if s.Mode == "paper" && value > s.Cash+1e-8 {
				return ai.halt(fmt.Errorf("insufficient paper balance"))
			}
			s.Quantity += quantity
			s.Cost += value
			if s.Mode == "paper" {
				s.Cash = math.Max(0, s.Cash-value)
			}
			s.StopLimit = s.Cost / s.Quantity * ai.settings.StopLimitPercent
		} else {
			if quantity > s.Quantity+1e-8 || s.Quantity <= 0 {
				return ai.halt(fmt.Errorf("execution exceeds the engine's position"))
			}
			average := s.Cost / s.Quantity
			s.RealizedProfit += value - average*quantity
			s.Quantity = math.Max(0, s.Quantity-quantity)
			s.Cost = average * s.Quantity
			if s.Mode == "paper" {
				s.Cash += value
			}
			if !ai.sellablePosition() {
				s.StopLimit = 0
				s.Params = nil
			}
		}
		// Price/Size are fee-adjusted ledger amounts. ExecutionPrice retains the
		// actual exchange quote for chart markers and trade inspection.
		s.Events = append(s.Events, models.SignalEvent{Time: p.BarTime, ProductCode: s.ProductCode, Side: p.Side, Price: value / quantity, Size: quantity, ExecutionPrice: price, Commission: commission})
	}
	s.Pending = nil
	s.Halted = false
	s.LastError = ""
	if err := ai.persist(); err != nil {
		return err
	}
	if executed > 0 {
		log.Printf("action=automation_fill mode=%s product=%s side=%s size=%.8f price=%g commission=%.8f", s.Mode, s.ProductCode, p.Side, executed, price, commission)
	}
	return nil
}
