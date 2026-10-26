package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"time"

	"gotrading/app/models"
	"gotrading/app/views"
	"gotrading/config"
)

var templates = template.Must(template.ParseFS(views.Files, "chart.html"))

type JSONError struct {
	Error string `json:"error"`
	Code  int    `json:"code"`
}

func APIError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(JSONError{Error: message, Code: code}); err != nil {
		log.Printf("action=APIError err=%s", err)
	}
}

func candleParameters(r *http.Request, requireProduct bool, defaultDuration string, defaultLimit int) (string, time.Duration, int, error) {
	query := r.URL.Query()
	productCode := query.Get("product_code")
	if productCode == "" && !requireProduct {
		productCode = config.Config.ProductCode
	}
	if productCode == "" {
		return "", 0, 0, fmt.Errorf("product_code is required")
	}
	if productCode != config.Config.ProductCode {
		return "", 0, 0, fmt.Errorf("unsupported product_code")
	}
	durationName := query.Get("duration")
	if durationName == "" {
		durationName = defaultDuration
	}
	duration, ok := config.Config.Durations[durationName]
	if !ok || duration <= 0 {
		return "", 0, 0, fmt.Errorf("duration must be 1s, 1m, or 1h")
	}
	limit := defaultLimit
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 1000 {
			return "", 0, 0, fmt.Errorf("limit must be an integer between 1 and 1000")
		}
		limit = parsed
	}
	return productCode, duration, limit, nil
}

func viewChartHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/chart/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defaultLimit := config.Config.DataLimit
	if defaultLimit == 0 {
		defaultLimit = 365
	}
	productCode, duration, limit, err := candleParameters(r, false, "1m", defaultLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	durationName := ""
	for name, value := range config.Config.Durations {
		if value == duration {
			durationName = name
			break
		}
	}
	page := struct {
		ProductCode     string
		Duration        string
		Limit           int
		DefaultStrategy string
	}{ProductCode: productCode, Duration: durationName, Limit: limit, DefaultStrategy: "auto"}
	var body bytes.Buffer
	if err := templates.ExecuteTemplate(&body, "chart.html", page); err != nil {
		log.Printf("action=viewChart render_err=%s", err)
		http.Error(w, "failed to render chart", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := body.WriteTo(w); err != nil {
		log.Printf("action=viewChart write_err=%s", err)
	}
}

func apiMakeHandler(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/candle/" {
			APIError(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			APIError(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fn(w, r)
	}
}

func apiCandleHandler(w http.ResponseWriter, r *http.Request) {
	productCode, duration, limit, err := candleParameters(r, true, "1m", 1000)
	if err != nil {
		APIError(w, err.Error(), http.StatusBadRequest)
		return
	}
	indicators, err := parseIndicators(r.URL.Query())
	if err != nil {
		APIError(w, err.Error(), http.StatusBadRequest)
		return
	}
	backtest, err := parseBacktest(r.URL.Query())
	if err != nil {
		APIError(w, err.Error(), http.StatusBadRequest)
		return
	}
	df, err := models.GetAllCandle(productCode, duration, limit)
	if err != nil {
		log.Printf("action=apiCandle err=%s", err)
		APIError(w, "failed to load candle data", http.StatusInternalServerError)
		return
	}
	for _, add := range indicators {
		add(df)
	}
	if ai := activeAI.Load(); ai != nil {
		if snapshot := ai.snapshot.Load(); snapshot != nil && snapshot.status.ProductCode == productCode {
			status := snapshot.status
			df.Automation = &status
		}
	}
	if err := backtest.apply(df); err != nil {
		log.Printf("action=apiCandle events_err=%s", err)
		APIError(w, "failed to load signal events", http.StatusInternalServerError)
		return
	}
	body, err := json.Marshal(df)
	if err != nil {
		log.Printf("action=apiCandle encode_err=%s", err)
		APIError(w, "failed to encode candle data", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(body); err != nil {
		log.Printf("action=apiCandle write_err=%s", err)
	}
}

func NewRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/chart/", viewChartHandler)
	mux.Handle("/static/", http.FileServer(http.FS(views.Files)))
	mux.HandleFunc("/api/candle/", apiMakeHandler(apiCandleHandler))
	mux.HandleFunc("/api/automation/", apiAutomationHandler)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		APIError(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/chart/", http.StatusSeeOther)
	})
	return mux
}

func StartWebServer(ctx context.Context) error {
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", config.Config.Port), Handler: NewRouter(),
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second,
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("action=shutdownWebServer err=%s", err)
			server.Close()
		}
	})
	defer func() {
		if !stop() {
			<-done
		}
	}()
	log.Printf("chart=http://localhost:%d/chart/", config.Config.Port)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
