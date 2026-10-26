package models

import (
	"database/sql"
	"fmt"
	"regexp"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const tableNameSignalEvents = "signal_events"

var DbConnection *sql.DB

var validProductCode = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)

func GetCandleTableName(productCode string, duration time.Duration) string {
	return fmt.Sprintf("%s_%s", productCode, duration)
}

func candleTable(productCode string, duration time.Duration) (string, error) {
	if !validProductCode.MatchString(productCode) || duration <= 0 {
		return "", fmt.Errorf("invalid candle product or duration")
	}
	return `"` + GetCandleTableName(productCode, duration) + `"`, nil
}

// InitDB is called explicitly at startup, so importing models has no side effects.
func InitDB(driver, filename, productCode string, durations map[string]time.Duration) error {
	if len(durations) == 0 {
		return fmt.Errorf("at least one candle duration is required")
	}
	db, err := sql.Open(driver, filename)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	// One connection also keeps an in-memory SQLite database consistent across queries.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ready := false
	defer func() {
		if !ready {
			db.Close()
		}
	}()
	if err := db.Ping(); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	for _, pragma := range []string{"PRAGMA busy_timeout = 5000", "PRAGMA journal_mode = WAL"} {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("configure database: %w", err)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS automation_state (
		mode TEXT NOT NULL,product_code TEXT NOT NULL,duration INTEGER NOT NULL,
		payload TEXT NOT NULL,version INTEGER NOT NULL,PRIMARY KEY(mode,product_code,duration))`); err != nil {
		return fmt.Errorf("create automation schema: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS ` + tableNameSignalEvents + ` (
		time DATETIME PRIMARY KEY NOT NULL,
		product_code TEXT, side TEXT, price REAL, size REAL)`); err != nil {
		return fmt.Errorf("create signal schema: %w", err)
	}
	for _, duration := range durations {
		table, err := candleTable(productCode, duration)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS ` + table + ` (
			time DATETIME PRIMARY KEY NOT NULL,
			open REAL NOT NULL, close REAL NOT NULL, high REAL NOT NULL,
			low REAL NOT NULL, volume REAL NOT NULL)`); err != nil {
			return fmt.Errorf("create candle schema: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	DbConnection = db
	ready = true
	return nil
}

func database() (*sql.DB, error) {
	if DbConnection == nil {
		return nil, fmt.Errorf("database is not initialized")
	}
	return DbConnection, nil
}
