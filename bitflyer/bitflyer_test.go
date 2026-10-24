package bitflyer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func mockHTTP(api *APIClient, status int, body string, check func(*http.Request)) {
	api.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		check(request)
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})
}

func checkSignature(t *testing.T, request *http.Request, body []byte) {
	t.Helper()
	if request.Header.Get("ACCESS-KEY") != "test-key" || request.Header.Get("ACCESS-TIMESTAMP") == "" {
		t.Error("private request is missing authentication headers")
	}
	mac := hmac.New(sha256.New, []byte("test-secret"))
	io.WriteString(mac, request.Header.Get("ACCESS-TIMESTAMP")+request.Method+request.URL.RequestURI())
	mac.Write(body)
	if request.Header.Get("ACCESS-SIGN") != hex.EncodeToString(mac.Sum(nil)) {
		t.Error("signature does not match the method, path, query and body sent")
	}
}

func TestGetTickerWithoutCredentials(t *testing.T) {
	api := New("XXXXX", "XXXXX")
	mockHTTP(api, http.StatusOK, `{"product_code":"BTC_USD","timestamp":"2018-10-12T03:01:53.8597609Z","tick_id":33839,"best_bid":6164,"best_ask":6206.99}`, func(request *http.Request) {
		if request.Method != http.MethodGet || request.URL.RequestURI() != "/v1/getticker?product_code=BTC_USD" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL)
		}
		if request.Header.Get("ACCESS-KEY") != "" || request.Header.Get("ACCESS-SIGN") != "" {
			t.Error("public ticker request should not send credentials")
		}
	})
	ticker, err := api.GetTicker("BTC_USD")
	if err != nil {
		t.Fatal(err)
	}
	if ticker.ProductCode != "BTC_USD" || ticker.TickID != 33839 || ticker.GetMidPrice() != (6164+6206.99)/2 {
		t.Fatalf("unexpected ticker: %+v", ticker)
	}
}

func TestTickerTimeFormats(t *testing.T) {
	for _, timestamp := range []string{"2018-10-12T03:01:53.8597609Z", "2018-10-12T03:01:53.8597609", "2018-10-12T12:01:53.8597609+09:00"} {
		t.Run(timestamp, func(t *testing.T) {
			ticker := Ticker{Timestamp: timestamp}
			want := time.Date(2018, 10, 12, 3, 1, 53, 859760900, time.UTC)
			if !ticker.DateTime().Equal(want) {
				t.Errorf("got %s, want %s", ticker.DateTime(), want)
			}
			if !ticker.TruncateDateTime(time.Minute).Equal(time.Date(2018, 10, 12, 3, 1, 0, 0, time.UTC)) {
				t.Error("minute truncation is incorrect")
			}
		})
	}
}

func TestPrivateGETRequests(t *testing.T) {
	t.Run("balance", func(t *testing.T) {
		api := New("test-key", "test-secret")
		mockHTTP(api, http.StatusOK, `[{"currency_code":"USD","amount":100,"available":90}]`, func(request *http.Request) {
			if request.URL.RequestURI() != "/v1/me/getbalance" {
				t.Errorf("unexpected balance endpoint: %s", request.URL)
			}
			checkSignature(t, request, nil)
		})
		balances, err := api.GetBalance()
		if err != nil || len(balances) != 1 || balances[0].CurrencyCode != "USD" || balances[0].Available != 90 {
			t.Fatalf("balances=%+v, err=%v", balances, err)
		}
	})
	t.Run("orders with signed query", func(t *testing.T) {
		api := New("test-key", "test-secret")
		mockHTTP(api, http.StatusOK, `[{"id":1,"child_order_acceptance_id":"JRF-test","child_order_state":"COMPLETED","executed_size":0.01}]`, func(request *http.Request) {
			if request.Method != http.MethodGet || request.URL.RequestURI() != "/v1/me/getchildorders?child_order_acceptance_id=JRF-test&product_code=BTC_USD" {
				t.Errorf("unexpected orders request: %s %s", request.Method, request.URL)
			}
			checkSignature(t, request, nil)
		})
		orders, err := api.ListOrder(map[string]string{"product_code": "BTC_USD", "child_order_acceptance_id": "JRF-test"})
		if err != nil || len(orders) != 1 || orders[0].ChildOrderState != "COMPLETED" || orders[0].ExecutedSize != 0.01 {
			t.Fatalf("orders=%+v, err=%v", orders, err)
		}
	})
}

func TestSendOrder(t *testing.T) {
	for _, side := range []string{"BUY", "SELL"} {
		t.Run(side, func(t *testing.T) {
			api := New("test-key", "test-secret")
			mockHTTP(api, http.StatusOK, `{"child_order_acceptance_id":"JRF-test"}`, func(request *http.Request) {
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				checkSignature(t, request, body)
				if request.Method != http.MethodPost || request.URL.Path != "/v1/me/sendchildorder" {
					t.Errorf("unexpected order request: %s %s", request.Method, request.URL)
				}
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil {
					t.Fatal(err)
				}
				if len(payload) != 7 || payload["side"] != side || payload["product_code"] != "BTC_USD" || payload["price"] != float64(7000) || payload["size"] != 0.01 {
					t.Errorf("unexpected order payload: %s", body)
				}
				if _, exists := payload["child_order_state"]; exists {
					t.Error("response fields must not be included in an order request")
				}
			})
			response, err := api.SendOrder(&Order{
				ProductCode: "BTC_USD", ChildOrderType: "LIMIT", Side: side, Price: 7000,
				Size: 0.01, MinuteToExpires: 1, TimeInForce: "GTC", ChildOrderState: "COMPLETED",
			})
			if err != nil || response == nil || response.ChildOrderAcceptanceID != "JRF-test" {
				t.Fatalf("response=%+v, err=%v", response, err)
			}
		})
	}
}

func TestHTTPFailureIsNotDecodedAsJSON(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			api := New("test-key", "test-secret")
			mockHTTP(api, status, "<html>error</html>", func(*http.Request) {})
			response, err := api.GetTicker("BTC_USD")
			if response != nil || err == nil || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", status)) {
				t.Fatalf("response=%+v, err=%v", response, err)
			}
		})
	}
}

func realtimeTestClient(t *testing.T, frames ...string) *APIClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.Close()
		connection.SetReadDeadline(time.Now().Add(5 * time.Second))
		var request struct {
			Version string `json:"jsonrpc"`
			Method  string `json:"method"`
			ID      int    `json:"id"`
			Params  struct {
				Channel string `json:"channel"`
			} `json:"params"`
		}
		if err := connection.ReadJSON(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Version != "2.0" || request.Method != "subscribe" || request.ID != 1 || request.Params.Channel != "lightning_ticker_BTC_USD" {
			t.Errorf("unexpected subscription request: %+v", request)
			return
		}
		for _, frame := range frames {
			if err := connection.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				t.Error(err)
				return
			}
		}
		// Keep the connection open until the client stops or its context is canceled.
		connection.ReadMessage()
	}))
	t.Cleanup(server.Close)
	api := New("XXXXX", "XXXXX")
	api.realtimeURL = "ws" + strings.TrimPrefix(server.URL, "http")
	return api
}

func TestRealtimeTickerAndCancellation(t *testing.T) {
	api := realtimeTestClient(t,
		`{"jsonrpc":"2.0","id":1,"result":true}`,
		`{"jsonrpc":"2.0","method":"channelMessage","params":{"channel":"lightning_ticker_OTHER","message":{}}}`,
		`{"jsonrpc":"2.0","method":"channelMessage","params":{"channel":"lightning_ticker_BTC_USD","message":{"product_code":"BTC_USD","best_bid":6164,"best_ask":6206.99,"tick_id":33839}}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tickers := make(chan Ticker)
	done := make(chan error, 1)
	go func() { done <- api.GetRealTimeTickerContext(ctx, "BTC_USD", tickers) }()
	select {
	case ticker, ok := <-tickers:
		if !ok || ticker.ProductCode != "BTC_USD" || ticker.TickID != 33839 {
			t.Fatalf("unexpected ticker: %+v, open=%v", ticker, ok)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for a ticker")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected cancellation error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not stop after cancellation")
	}
	if _, open := <-tickers; open {
		t.Error("ticker channel was not closed")
	}
}

func TestRealtimeSubscriptionFailures(t *testing.T) {
	for _, frame := range []string{
		`{"jsonrpc":"2.0","id":1,"result":false}`,
		`{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"invalid channel"}}`,
	} {
		t.Run(frame, func(t *testing.T) {
			api := realtimeTestClient(t, frame)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			tickers := make(chan Ticker)
			err := api.GetRealTimeTickerContext(ctx, "BTC_USD", tickers)
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected subscription error, got %v", err)
			}
			if _, open := <-tickers; open {
				t.Error("ticker channel was not closed on failure")
			}
		})
	}
}
