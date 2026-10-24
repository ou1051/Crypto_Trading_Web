package bitflyer

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const baseURL = "https://api.bitflyer.com"

type APIClient struct {
	key        string
	secret     string
	httpClient *http.Client
}

func New(key, secret string) *APIClient {
	api := &APIClient{
		key:        key,
		secret:     secret,
		httpClient: &http.Client{Timeout: 15 * time.Second},
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
	request, err := http.NewRequest(method, endpoint, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	q := request.URL.Query()
	for key, value := range query {
		q.Add(key, value)
	}
	request.URL.RawQuery = q.Encode()

	for key, value := range api.header(method, request.URL.RequestURI(), requestBody) {
		request.Header.Set(key, value)
	}
	response, err := api.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("bitflyer: %s %s: HTTP %s", method, request.URL.RequestURI(), response.Status)
	}
	responseBody, err = ioutil.ReadAll(response.Body)
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
	urlPath := "/v1/me/getbalance"
	responseBody, err := api.doRequest("GET", urlPath, map[string]string{}, nil)
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
