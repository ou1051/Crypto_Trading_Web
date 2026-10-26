package bitflyer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const baseURL = "https://api.bitflyer.com"
const realtimeURL = "wss://ws.lightstream.bitflyer.com/json-rpc"

type APIClient struct {
	key         string
	secret      string
	httpClient  *http.Client
	realtimeURL string
}

func New(key, secret string) *APIClient {
	api := &APIClient{
		key:         key,
		secret:      secret,
		httpClient:  &http.Client{Timeout: 15 * time.Second},
		realtimeURL: realtimeURL,
	}
	return api
}

func (api *APIClient) header(method, endpoint string, body []byte) map[string]string {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	message := timestamp + method + endpoint + string(body)

	mac := hmac.New(sha256.New, []byte(api.secret))
	mac.Write([]byte(message))
	sign := hex.EncodeToString(mac.Sum(nil))
	return map[string]string{
		"ACCESS-KEY":       api.key,
		"ACCESS-TIMESTAMP": timestamp,
		"ACCESS-SIGN":      sign,
		"Content-Type":     "application/json",
	}
}

func (api *APIClient) doRequest(method, urlPath string, query map[string]string, requestBody []byte) (responseBody []byte, err error) {
	return api.doRequestContext(context.Background(), method, urlPath, query, requestBody)
}

func (api *APIClient) doRequestContext(ctx context.Context, method, urlPath string, query map[string]string, requestBody []byte) (responseBody []byte, err error) {
	baseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	apiURL, err := baseURL.Parse(urlPath)
	if err != nil {
		return nil, err
	}
	endpoint := baseURL.ResolveReference(apiURL).String()
	log.Printf("action=doRequest endpoint=%s", endpoint)
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	q := request.URL.Query()
	for key, value := range query {
		q.Add(key, value)
	}
	request.URL.RawQuery = q.Encode()

	request.Header.Set("Content-Type", "application/json")
	if strings.HasPrefix(request.URL.Path, "/v1/me/") {
		for key, value := range api.header(method, request.URL.RequestURI(), requestBody) {
			request.Header.Set(key, value)
		}
	}
	response, err := api.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("bitflyer: %s %s: HTTP %s", method, request.URL.RequestURI(), response.Status)
	}
	responseBody, err = io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return responseBody, nil
}

type Balance struct {
	CurrencyCode string  `json:"currency_code"`
	Amount       float64 `json:"amount"`
	Available    float64 `json:"available"`
}

func (api *APIClient) GetBalance() ([]Balance, error) {
	return api.GetBalanceContext(context.Background())
}

func (api *APIClient) GetBalanceContext(ctx context.Context) ([]Balance, error) {
	urlPath := "/v1/me/getbalance"
	responseBody, err := api.doRequestContext(ctx, "GET", urlPath, map[string]string{}, nil)
	log.Printf("action=GetBalance response=%s", string(responseBody))
	if err != nil {
		log.Printf("action=GetBalance err=%s", err.Error())
		return nil, err
	}

	var balances []Balance
	err = json.Unmarshal(responseBody, &balances)
	if err != nil {
		log.Printf("action=GetBalance err=%s", err.Error())
		return nil, err
	}

	return balances, nil
}

type Ticker struct {
	ProductCode     string  `json:"product_code"`
	Timestamp       string  `json:"timestamp"`
	TickID          int     `json:"tick_id"`
	BestBid         float64 `json:"best_bid"`
	BestAsk         float64 `json:"best_ask"`
	BestBidSize     float64 `json:"best_bid_size"`
	BestAskSize     float64 `json:"best_ask_size"`
	TotalBidDepth   float64 `json:"total_bid_depth"`
	TotalAskDepth   float64 `json:"total_ask_depth"`
	Ltp             float64 `json:"ltp"`
	Volume          float64 `json:"volume"`
	VolumeByProduct float64 `json:"volume_by_product"`
}

func (t *Ticker) GetMidPrice() float64 {
	return (t.BestBid + t.BestAsk) / 2
}

func (t *Ticker) DateTime() time.Time {
	dateTime, err := time.Parse(time.RFC3339Nano, t.Timestamp)
	if err != nil {
		// HTTP API timestamps can omit the UTC timezone suffix.
		dateTime, err = time.ParseInLocation("2006-01-02T15:04:05.999999999", t.Timestamp, time.UTC)
	}
	if err != nil {
		log.Printf("action=DateTime err=%s", err)
	}
	return dateTime
}

func (t *Ticker) TruncateDateTime(duration time.Duration) time.Time {
	return t.DateTime().Truncate(duration)
}

func (api *APIClient) GetTicker(productCode string) (*Ticker, error) {
	return api.GetTickerContext(context.Background(), productCode)
}

func (api *APIClient) GetTickerContext(ctx context.Context, productCode string) (*Ticker, error) {
	responseBody, err := api.doRequestContext(ctx, http.MethodGet, "/v1/getticker", map[string]string{"product_code": productCode}, nil)
	if err != nil {
		return nil, err
	}
	var ticker Ticker
	if err := json.Unmarshal(responseBody, &ticker); err != nil {
		return nil, err
	}
	return &ticker, nil
}

type JsonRPC2 struct {
	Version string          `json:"jsonrpc"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	ID      *int            `json:"id,omitempty"`
	Error   *JsonRPCError   `json:"error,omitempty"`
}

type JsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type SubscribeParams struct {
	Channel string `json:"channel"`
}

// GetRealTimeTicker sends tickers until the connection ends, then closes ch.
func (api *APIClient) GetRealTimeTicker(symbol string, ch chan<- Ticker) error {
	return api.GetRealTimeTickerContext(context.Background(), symbol, ch)
}

// GetRealTimeTickerContext also lets callers stop the stream using ctx.
// The caller must not close ch; this method closes it when the stream ends.
func (api *APIClient) GetRealTimeTickerContext(ctx context.Context, symbol string, ch chan<- Ticker) error {
	defer close(ch)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 15 * time.Second
	connection, response, err := dialer.DialContext(ctx, api.realtimeURL, nil)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		return fmt.Errorf("bitflyer: connect realtime: %w", err)
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	connection.SetReadLimit(1 << 20)
	if err := connection.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	if err := connection.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}

	channel := fmt.Sprintf("lightning_ticker_%s", symbol)
	params, err := json.Marshal(SubscribeParams{Channel: channel})
	if err != nil {
		return err
	}
	id := 1
	if err := connection.WriteJSON(&JsonRPC2{Version: "2.0", Method: "subscribe", Params: params, ID: &id}); err != nil {
		return fmt.Errorf("bitflyer: subscribe: %w", err)
	}

	for {
		var message JsonRPC2
		if err := connection.ReadJSON(&message); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("bitflyer: read realtime: %w", err)
		}
		if message.Error != nil {
			return fmt.Errorf("bitflyer: realtime RPC error %d: %s", message.Error.Code, message.Error.Message)
		}
		if message.ID != nil && *message.ID == id {
			var subscribed bool
			if err := json.Unmarshal(message.Result, &subscribed); err != nil || !subscribed {
				return fmt.Errorf("bitflyer: subscription rejected for %s", channel)
			}
			if err := connection.SetReadDeadline(time.Time{}); err != nil {
				return err
			}
			continue
		}
		if message.Method != "channelMessage" {
			continue
		}
		var notification struct {
			Channel string          `json:"channel"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(message.Params, &notification); err != nil {
			return fmt.Errorf("bitflyer: decode notification: %w", err)
		}
		if notification.Channel != channel {
			continue
		}
		var ticker Ticker
		if err := json.Unmarshal(notification.Message, &ticker); err != nil {
			return fmt.Errorf("bitflyer: decode ticker: %w", err)
		}
		select {
		case ch <- ticker:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type Order struct {
	ID                     int     `json:"id"`
	ChildOrderAcceptanceID string  `json:"child_order_acceptance_id"`
	ProductCode            string  `json:"product_code"`
	ChildOrderType         string  `json:"child_order_type"`
	Side                   string  `json:"side"`
	Price                  float64 `json:"price"`
	Size                   float64 `json:"size"`
	MinuteToExpires        int     `json:"minute_to_expire"`
	TimeInForce            string  `json:"time_in_force"`
	AveragePrice           float64 `json:"average_price"`
	ChildOrderState        string  `json:"child_order_state"`
	ExpireDate             string  `json:"expire_date"`
	ChildOrderDate         string  `json:"child_order_date"`
	OutstandingSize        float64 `json:"outstanding_size"`
	CancelSize             float64 `json:"cancel_size"`
	ExecutedSize           float64 `json:"executed_size"`
	TotalCommission        float64 `json:"total_commission"`
}

type ResponseSendChildOrder struct {
	ChildOrderAcceptanceID string `json:"child_order_acceptance_id"`
}

func (api *APIClient) SendOrder(order *Order) (*ResponseSendChildOrder, error) {
	return api.SendOrderContext(context.Background(), order)
}

func (api *APIClient) SendOrderContext(ctx context.Context, order *Order) (*ResponseSendChildOrder, error) {
	if order == nil {
		return nil, fmt.Errorf("bitflyer: order is required")
	}
	// Only send order parameters; fields returned by ListOrder are not request fields.
	request := struct {
		ProductCode     string  `json:"product_code"`
		ChildOrderType  string  `json:"child_order_type"`
		Side            string  `json:"side"`
		Price           float64 `json:"price,omitempty"`
		Size            float64 `json:"size"`
		MinuteToExpires int     `json:"minute_to_expire,omitempty"`
		TimeInForce     string  `json:"time_in_force,omitempty"`
	}{order.ProductCode, order.ChildOrderType, order.Side, order.Price, order.Size, order.MinuteToExpires, order.TimeInForce}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	responseBody, err := api.doRequestContext(ctx, http.MethodPost, "/v1/me/sendchildorder", nil, data)
	if err != nil {
		return nil, err
	}
	var response ResponseSendChildOrder
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (api *APIClient) ListOrder(query map[string]string) ([]Order, error) {
	return api.ListOrderContext(context.Background(), query)
}

func (api *APIClient) ListOrderContext(ctx context.Context, query map[string]string) ([]Order, error) {
	responseBody, err := api.doRequestContext(ctx, http.MethodGet, "/v1/me/getchildorders", query, nil)
	if err != nil {
		return nil, err
	}
	var orders []Order
	if err := json.Unmarshal(responseBody, &orders); err != nil {
		return nil, err
	}
	return orders, nil
}

func (api *APIClient) CancelOrderContext(ctx context.Context, productCode, acceptanceID string) error {
	if productCode == "" || acceptanceID == "" {
		return fmt.Errorf("bitflyer: product and order ID are required")
	}
	data, err := json.Marshal(struct {
		ProductCode  string `json:"product_code"`
		AcceptanceID string `json:"child_order_acceptance_id"`
	}{productCode, acceptanceID})
	if err != nil {
		return err
	}
	_, err = api.doRequestContext(ctx, http.MethodPost, "/v1/me/cancelchildorder", nil, data)
	return err
}
