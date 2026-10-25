package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gotrading/app/controllers"
	"gotrading/app/models"
	"gotrading/bitflyer"
	"gotrading/config"
	"gotrading/utils"
)

func main() {
	mode := flag.String("mode", "web", "web, ticker, realtime, balance, order, or orders")
	side := flag.String("side", "BUY", "BUY or SELL (order mode)")
	price := flag.Float64("price", 0, "limit price (order mode)")
	size := flag.Float64("size", 0, "order size in BTC (order mode)")
	flag.Parse()

	settings, err := config.Load("config.ini")
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	config.Config = settings
	utils.LoggingSettings(config.Config.LogFile)
	apiClient := bitflyer.New(config.Config.ApiKey, config.Config.ApiSecret)
	if err := run(apiClient, *mode, *side, *price, *size); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(apiClient *bitflyer.APIClient, mode, side string, price, size float64) error {
	switch mode {
	case "balance", "order", "orders":
		for _, value := range []string{config.Config.ApiKey, config.Config.ApiSecret} {
			if value == "" || strings.Trim(value, "X") == "" {
				return fmt.Errorf("%s mode requires an API key and secret; use ticker or realtime with XXXXX credentials", mode)
			}
		}
	}

	switch mode {
	case "web":
		return runWeb()
	case "ticker":
		ticker, err := apiClient.GetTicker(config.Config.ProductCode)
		if err != nil {
			return err
		}
		printTicker(*ticker)
	case "realtime":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		tickerChannel := make(chan bitflyer.Ticker)
		errChannel := make(chan error, 1)
		go func() {
			errChannel <- apiClient.GetRealTimeTickerContext(ctx, config.Config.ProductCode, tickerChannel)
		}()
		for ticker := range tickerChannel {
			printTicker(ticker)
		}
		if err := <-errChannel; err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	case "balance":
		balances, err := apiClient.GetBalance()
		if err != nil {
			return err
		}
		fmt.Println(balances)
	case "order":
		if price <= 0 || size <= 0 || (side != "BUY" && side != "SELL") {
			return fmt.Errorf("order mode requires -price > 0, -size > 0, and -side BUY or SELL")
		}
		order := &bitflyer.Order{
			ProductCode:     config.Config.ProductCode,
			ChildOrderType:  "LIMIT",
			Side:            side,
			Price:           price,
			Size:            size,
			MinuteToExpires: 1,
			TimeInForce:     "GTC",
		}
		response, err := apiClient.SendOrder(order)
		if err != nil {
			return err
		}
		fmt.Println(response.ChildOrderAcceptanceID)
	case "orders":
		orders, err := apiClient.ListOrder(map[string]string{"product_code": config.Config.ProductCode})
		if err != nil {
			return err
		}
		fmt.Println(orders)
	default:
		return fmt.Errorf("unknown mode %q; choose web, ticker, realtime, balance, order, or orders", mode)
	}
	return nil
}

func runWeb() error {
	settings := config.Config
	if err := models.InitDB(settings.SQLDriver, settings.DbName, settings.ProductCode, settings.Durations); err != nil {
		return err
	}
	defer models.DbConnection.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 2)
	go func() { done <- controllers.StreamIngestionData(ctx) }()
	go func() { done <- controllers.StartWebServer(ctx) }()
	err := <-done
	stop()
	otherErr := <-done
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if otherErr != nil && !errors.Is(otherErr, context.Canceled) {
		return otherErr
	}
	return nil
}

func printTicker(ticker bitflyer.Ticker) {
	fmt.Printf("%s mid_price=%f timestamp=%s second=%s minute=%s hour=%s\n",
		ticker.ProductCode, ticker.GetMidPrice(), ticker.DateTime().Format(time.RFC3339Nano),
		ticker.TruncateDateTime(time.Second).Format(time.RFC3339),
		ticker.TruncateDateTime(time.Minute).Format(time.RFC3339),
		ticker.TruncateDateTime(time.Hour).Format(time.RFC3339))
}
