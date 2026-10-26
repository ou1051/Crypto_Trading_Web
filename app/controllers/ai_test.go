package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gotrading/app/models"
	"gotrading/bitflyer"
	"gotrading/config"
)

type fakeBroker struct {
	calls                []string
	orders               []bitflyer.Order
	balances             []bitflyer.Balance
	quote                *bitflyer.Ticker
	history              []bitflyer.Order
	sendError, listError error
	onSend               func()
}

func (b *fakeBroker) GetBalanceContext(context.Context) ([]bitflyer.Balance, error) {
	b.calls = append(b.calls, "balance")
	return b.balances, nil
}
func (b *fakeBroker) GetTickerContext(context.Context, string) (*bitflyer.Ticker, error) {
	b.calls = append(b.calls, "ticker")
	return b.quote, nil
}
func (b *fakeBroker) SendOrderContext(_ context.Context, order *bitflyer.Order) (*bitflyer.ResponseSendChildOrder, error) {
	b.calls = append(b.calls, "send")
	b.orders = append(b.orders, *order)
	if b.onSend != nil {
		b.onSend()
	}
	if b.sendError != nil {
		return nil, b.sendError
	}
	return &bitflyer.ResponseSendChildOrder{ChildOrderAcceptanceID: "JRF-owned"}, nil
}
func (b *fakeBroker) ListOrderContext(_ context.Context, query map[string]string) ([]bitflyer.Order, error) {
	b.calls = append(b.calls, "list:"+query["child_order_acceptance_id"])
	return b.history, b.listError
}
func (b *fakeBroker) CancelOrderContext(_ context.Context, product, id string) error {
	b.calls = append(b.calls, "cancel:"+product+":"+id)
	return nil
}

func setupAI(t *testing.T, live bool) (*AI, *fakeBroker, bitflyer.Ticker) {
	t.Helper()
	setupApplication(t)
	settings := config.Config
	settings.BackTest, settings.ApiKey, settings.ApiSecret = !live, "test-key", "test-secret"
	settings.DataLimit, settings.NumRanking = 365, 3
	settings.UsePercent, settings.StopLimitPercent = .5, .9
	settings.PaperBalance, settings.FeeRate = 100000, .01
	settings.SlippageRate = 0 // Existing accounting fixtures isolate fees from price impact.
	now := time.Now().UTC().Truncate(time.Minute).Add(time.Second)
	quote := streamSample(now, 1, 10000, 1000)
	quote.BestAsk, quote.BestBid = 10000, 9990
	broker := &fakeBroker{quote: &quote, balances: []bitflyer.Balance{{CurrencyCode: "USD", Available: 100000}, {CurrencyCode: "BTC", Available: 50}}}
	ai, err := NewAI(settings, broker)
	if err != nil {
		t.Fatal(err)
	}
	ai.now = func() time.Time { return now }
	ai.state.LastBar = now.Truncate(time.Minute)
	if err := ai.persist(); err != nil {
		t.Fatal(err)
	}
	return ai, broker, quote
}

func near(t *testing.T, actual, want float64) {
	t.Helper()
	if math.Abs(actual-want) > 1e-7 {
		t.Fatalf("got %.12f, want %.12f", actual, want)
	}
}

func TestPaperAutomationAccountsForFeesStopAndRestart(t *testing.T) {
	ai, broker, quote := setupAI(t, false)
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err != nil {
		t.Fatal(err)
	}
	s := ai.state
	near(t, s.Quantity, 4.95)
	near(t, s.Cash, 50000)
	near(t, s.StopLimit, 50000/4.95*.9)
	if len(s.Events) != 1 || s.Events[0].ExecutionPrice != 10000 || len(broker.calls) != 0 {
		t.Fatalf("paper order called exchange: %+v", broker)
	}
	// A stop must sell even when the model has no sell signal.
	s.LastBar = s.LastBar.Add(time.Minute)
	quote.BestBid = 9000
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{}, s.StopLimit-1); err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 2 || s.Events[1].Side != "SELL" || s.Quantity >= minimumBTC || s.StopLimit != 0 || s.Params != nil {
		t.Fatalf("stop did not close position: %+v", s)
	}
	ledger := &models.SignalEvents{Signals: s.Events}
	near(t, ledger.Profit(), s.RealizedProfit)
	near(t, s.Cash+s.Cost-ai.settings.PaperBalance, s.RealizedProfit)
	before, _ := json.Marshal(s)
	restored, err := NewAI(ai.settings, broker)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(restored.state)
	if string(before) != string(after) {
		t.Fatalf("restart changed journal:\n%s\n%s", before, after)
	}
	liveSettings := ai.settings
	liveSettings.BackTest = false
	live, err := NewAI(liveSettings, broker)
	if err != nil || live.state.Quantity != 0 || len(live.state.Events) != 0 {
		t.Fatalf("paper state leaked into live: %+v %v", live, err)
	}
	var saved int
	if err := models.DbConnection.QueryRow(`SELECT COUNT(*) FROM signal_events`).Scan(&saved); err != nil || saved != 0 {
		t.Fatal("automation must not mix modes in legacy event storage", err)
	}
}

func TestLiveAutomationJournalsBeforeSendingAndUsesActualPartialFills(t *testing.T) {
	ai, broker, quote := setupAI(t, true)
	broker.onSend = func() {
		state, err := models.LoadAutomationState("live", "BTC_USD", time.Minute)
		if err != nil || state.Pending == nil || state.Pending.AcceptanceID != "" {
			t.Fatalf("order was not claimed before send: %+v %v", state, err)
		}
	}
	broker.history = []bitflyer.Order{{ChildOrderAcceptanceID: "JRF-other", ProductCode: "BTC_USD", Side: "BUY", ChildOrderState: "COMPLETED", ExecutedSize: 99, AveragePrice: 100},
		{ChildOrderAcceptanceID: "JRF-owned", ProductCode: "BTC_USD", Side: "BUY", ChildOrderState: "CANCELED", Size: 5, ExecutedSize: 2, AveragePrice: 10000, TotalCommission: .02}}
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err != nil {
		t.Fatal(err)
	}
	near(t, ai.state.Quantity, 1.98)
	near(t, ai.state.Cost, 20000)
	if ai.state.Pending != nil || len(broker.orders) != 1 || broker.orders[0].Size != 5 || broker.orders[0].ChildOrderType != "MARKET" {
		t.Fatalf("unexpected execution: %+v", ai.state)
	}
	// The account owns 50 BTC, but this engine must sell only its own 1.98 BTC.
	ai.state.LastBar = ai.state.LastBar.Add(time.Minute)
	broker.history = nil
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{SellVotes: 1}, 10000); err != nil {
		t.Fatal(err)
	}
	if len(broker.orders) != 2 || broker.orders[1].Size != btcFloor(1.98/1.01) {
		t.Fatalf("sold unrelated holdings: %+v", broker.orders)
	}
	p := ai.state.Pending
	broker.history = []bitflyer.Order{{ChildOrderAcceptanceID: p.AcceptanceID, ProductCode: "BTC_USD", Side: "SELL", ChildOrderState: "EXPIRED", Size: p.Size, ExecutedSize: 1, AveragePrice: 12000, TotalCommission: .01}}
	if err := ai.checkOrder(context.Background()); err != nil {
		t.Fatal(err)
	}
	near(t, ai.state.Quantity, .97)
	near(t, ai.state.RealizedProfit, 12000-20000/1.98*1.01)
	near(t, (&models.SignalEvents{Signals: ai.state.Events}).Profit(), ai.state.RealizedProfit)
}

func TestUnknownSubmissionNeverResendsIncludingAfterRestart(t *testing.T) {
	ai, broker, quote := setupAI(t, true)
	broker.sendError = context.DeadlineExceeded
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err == nil {
		t.Fatal("ambiguous send must halt")
	}
	if !ai.state.Halted || ai.state.Pending == nil || len(ai.state.Events) != 0 {
		t.Fatalf("unknown submission was lost: %+v", ai.state)
	}
	for i := 0; i < 3; i++ {
		ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000)
	}
	restarted, err := NewAI(ai.settings, broker)
	if err != nil {
		t.Fatal(err)
	}
	restarted.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000)
	restarted.checkOrder(context.Background())
	if len(broker.orders) != 1 || !restarted.state.Halted || restarted.state.Pending == nil {
		t.Fatal("uncertain order was retried")
	}
}

func TestAcceptedOrderRecoveryCancellationAndEmptyResponses(t *testing.T) {
	ai, broker, quote := setupAI(t, true)
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err != nil {
		t.Fatal(err)
	}
	created := ai.state.Pending.CreatedAt
	restarted, err := NewAI(ai.settings, broker)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = func() time.Time { return created.Add(90 * time.Second) }
	if err := restarted.checkOrder(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.checkOrder(context.Background())
	cancels := 0
	for _, call := range broker.calls {
		if strings.HasPrefix(call, "cancel:") {
			cancels++
			if call != "cancel:BTC_USD:JRF-owned" {
				t.Fatal(call)
			}
		}
	}
	if cancels != 1 || restarted.state.Pending == nil || len(broker.orders) != 1 {
		t.Fatal("empty response caused a retry or lost pending order", broker.calls)
	}
	restarted.now = func() time.Time { return created.Add(4 * time.Minute) }
	if err := restarted.checkOrder(context.Background()); err == nil || !restarted.state.Halted {
		t.Fatal("unresolved order must halt")
	}
	broker.history = []bitflyer.Order{{ChildOrderAcceptanceID: "JRF-owned", ProductCode: "BTC_USD", Side: "BUY", ChildOrderState: "COMPLETED", ExecutedSize: 5, AveragePrice: 10000, TotalCommission: .05}}
	if err := restarted.checkOrder(context.Background()); err != nil {
		t.Fatal(err)
	}
	if restarted.state.Pending != nil || restarted.state.Halted || restarted.state.Quantity != 4.95 || len(broker.orders) != 1 {
		t.Fatalf("reconciliation failed: %+v", restarted.state)
	}
}

func TestJournalConflictPreventsExternalActions(t *testing.T) {
	ai, broker, quote := setupAI(t, true)
	other, err := models.LoadAutomationState("live", "BTC_USD", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := models.SaveAutomationState(other); err != nil {
		t.Fatal(err)
	}
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); !errors.Is(err, models.ErrAutomationConflict) {
		t.Fatalf("expected version conflict, got %v", err)
	}
	ai.checkOrder(context.Background())
	ai.cancelPending(context.Background())
	if len(broker.orders) != 0 || !ai.state.Halted || !ai.storageFailed {
		t.Fatal("lost journal claim still submitted order")
	}
}

func TestClosedCandlesOnlyOneDecisionPerBarAndNoReplay(t *testing.T) {
	ai, broker, quote := setupAI(t, false)
	bar := ai.state.LastBar
	ai.state.LastBar = bar.Add(-time.Minute)
	ai.state.Params = &models.TradeParams{EmaEnable: true, EmaPeriod1: 2, EmaPeriod2: 3}
	// The final completed close produces a golden cross. The open candle has an
	// extreme opposite close; it must have no influence on the decision.
	for i, price := range []float64{10, 9, 8, 7, 6, 5, 20, 1} {
		stamp := bar.Add(time.Duration(i-7) * time.Minute)
		if err := models.NewCandle("BTC_USD", time.Minute, stamp, price, price, price, price, 1).Create(); err != nil {
			t.Fatal(err)
		}
	}
	if err := ai.onBar(context.Background(), quote); err != nil {
		t.Fatal(err)
	}
	if len(ai.state.Events) != 1 || ai.state.Events[0].Side != "BUY" {
		t.Fatalf("completed cross was missed: %+v", ai.state)
	}
	if err := ai.onBar(context.Background(), quote); err != nil {
		t.Fatal(err)
	}
	if len(ai.state.Events) != 1 || len(broker.calls) != 0 {
		t.Fatal("same bar was executed twice")
	}
	before := ai.state.Version
	old := quote
	old.Timestamp = bar.Add(-time.Minute).Format(time.RFC3339)
	ai.onBar(context.Background(), old)
	future := quote
	future.Timestamp = bar.Add(time.Minute).Format(time.RFC3339)
	ai.onBar(context.Background(), future)
	if ai.state.Version != before {
		t.Fatal("stale/future tick was acted on")
	}
	restarted, err := NewAI(ai.settings, broker)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = ai.now
	restarted.onBar(context.Background(), quote)
	if len(restarted.state.Events) != 1 {
		t.Fatal("restart replayed historical signal")
	}
}

func TestAutomationRejectsConflictingSignalsInvalidCredentialsAndTinyOrders(t *testing.T) {
	ai, broker, quote := setupAI(t, false)
	ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1, SellVotes: 1}, 10000)
	if len(ai.state.Events) != 0 {
		t.Fatal("conflicting signals opened a position")
	}
	ai.state.Cash = .01
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err == nil || ai.state.Pending != nil {
		t.Fatal("subminimum order was submitted")
	}
	settings := ai.settings
	settings.BackTest = false
	settings.ApiKey = "XXXXX"
	if _, err := NewAI(settings, broker); err == nil {
		t.Fatal("live placeholders were accepted")
	}
	settings = ai.settings
	settings.ProductCode = "FX_BTC_JPY"
	if _, err := NewAI(settings, broker); err == nil {
		t.Fatal("unsupported margin product was accepted")
	}
}

func TestFeeReserveDoesNotStrandAnUnsellableMinimumPosition(t *testing.T) {
	ai, _, quote := setupAI(t, false)
	// 0.001 BTC cannot fund both a minimum-sized sale and its BTC commission.
	// Treat it as dust; a later buy may replenish it instead of getting stuck in
	// an endless attempt to submit an undersized sell order.
	ai.state.Quantity = minimumBTC
	ai.state.Cost = minimumBTC * 10000
	if ai.sellablePosition() {
		t.Fatal("commission reserve was ignored")
	}
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err != nil {
		t.Fatal(err)
	}
	if len(ai.state.Events) != 1 || ai.state.Events[0].Side != "BUY" || !ai.sellablePosition() {
		t.Fatal("unsellable position prevented future trades")
	}
}

func TestPaperSlippageUsesAdversePricesAndPreservesCashBudget(t *testing.T) {
	ai, broker, quote := setupAI(t, false)
	ai.settings.SlippageRate = .01
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err != nil {
		t.Fatal(err)
	}
	if ai.state.Events[0].ExecutionPrice != 10100 || ai.state.Cash < 50000 {
		t.Fatal("slippage did not affect entry sizing", ai.state.Events)
	}
	ai.state.LastBar = ai.state.LastBar.Add(time.Minute)
	quote.BestBid = 12000
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{SellVotes: 1}, 12000); err != nil {
		t.Fatal(err)
	}
	if len(ai.state.Events) != 2 || ai.state.Events[1].ExecutionPrice != 11880 || len(broker.calls) != 0 {
		t.Fatal("paper exit did not use adverse slippage", ai.state.Events)
	}
	near(t, (&models.SignalEvents{Signals: ai.state.Events}).Profit(), ai.state.RealizedProfit)
}

func TestChangedResearchPolicyInvalidatesParametersWithoutLosingOrders(t *testing.T) {
	ai, broker, quote := setupAI(t, true)
	ai.state.Params = &models.TradeParams{EmaEnable: true, EmaPeriod1: 7, EmaPeriod2: 14}
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err != nil {
		t.Fatal(err)
	}
	settings := ai.settings
	settings.SlippageRate = .002
	restarted, err := NewAI(settings, broker)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.state.Params != nil || restarted.state.Pending == nil || restarted.state.Pending.AcceptanceID != "JRF-owned" || len(broker.orders) != 1 {
		t.Fatal("policy update lost order state or reused old parameters")
	}
}

func TestStopRemainsAvailableWithoutEnoughIndicatorHistory(t *testing.T) {
	ai, broker, quote := setupAI(t, false)
	bar := ai.state.LastBar
	ai.state.LastBar = bar.Add(-time.Minute)
	ai.state.Quantity = .1
	ai.state.Cost = 1000
	ai.state.StopLimit = 9000
	if err := models.NewCandle("BTC_USD", time.Minute, bar.Add(-time.Minute), 5000, 5000, 5000, 5000, 1).Create(); err != nil {
		t.Fatal(err)
	}
	quote.BestBid = 4900
	if err := ai.onBar(context.Background(), quote); err != nil {
		t.Fatal(err)
	}
	if len(ai.state.Events) != 1 || ai.state.Events[0].Side != "SELL" || len(broker.calls) != 0 {
		t.Fatal("indicator warmup disabled the stop")
	}
}

func TestStaleQuoteCannotSubmitAnOrderWithinTheSameHourBar(t *testing.T) {
	ai, broker, quote := setupAI(t, true)
	ai.settings.TradeDuration = time.Hour
	ai.state.Duration = time.Hour
	now := ai.now().Truncate(time.Hour).Add(30 * time.Minute)
	ai.now = func() time.Time { return now }
	ai.state.LastBar = ai.now().Truncate(time.Hour)
	ai.state.Version = 0
	if err := ai.persist(); err != nil {
		t.Fatal(err)
	}
	quote.Timestamp = ai.now().Add(-time.Minute).Format(time.RFC3339)
	if err := ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000); err == nil {
		t.Fatal("stale quote was accepted")
	}
	if len(broker.calls) != 0 || ai.state.Pending != nil || ai.storageFailed || !strings.Contains(ai.state.LastError, "stale") {
		t.Fatal("stale decision reached the exchange")
	}
}

func TestAutomationStatusAndEventAPI(t *testing.T) {
	ai, _, quote := setupAI(t, false)
	previous := activeAI.Swap(ai)
	t.Cleanup(func() { activeAI.Store(previous) })
	ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000)
	for _, endpoint := range []string{"/api/automation/", "/api/candle/?product_code=BTC_USD&events=true&strategy=auto"} {
		w := httptest.NewRecorder()
		NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, endpoint, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", endpoint, w.Code, w.Body)
		}
		if strings.Contains(endpoint, "candle") {
			var df models.DataFrameCandle
			if json.Unmarshal(w.Body.Bytes(), &df) != nil || df.Automation.Mode != "paper" || !reflect.DeepEqual(df.Events.Signals, ai.state.Events) {
				t.Fatalf("missing auto state/events: %s", w.Body)
			}
		} else {
			var status models.AutomationStatus
			if json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Quantity != 4.95 || status.FeeRate != .01 {
				t.Fatalf("bad status: %s", w.Body)
			}
		}
	}
	w := httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/automation/", nil))
	if w.Code != 405 {
		t.Fatal("status endpoint must be read only")
	}
	activeAI.Store(nil)
	w = httptest.NewRecorder()
	NewRouter().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/automation/", nil))
	if w.Code != 503 {
		t.Fatal("missing engine must return service unavailable")
	}
}

func TestAutomationRunCancelsOwnPendingOrderOnShutdown(t *testing.T) {
	ai, broker, quote := setupAI(t, true)
	ai.executeDecision(context.Background(), quote, models.TradeDecision{BuyVotes: 1}, 10000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { defer close(done); ai.Run(ctx, make(chan bitflyer.Ticker)) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation did not stop")
	}
	if ai.state.Pending == nil || !ai.state.Pending.CancelRequested || broker.calls[len(broker.calls)-1] != "cancel:BTC_USD:JRF-owned" {
		t.Fatal("shutdown lost the pending order", broker.calls)
	}
}
