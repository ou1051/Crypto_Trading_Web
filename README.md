# GoTrading

A Go application for collecting Bitcoin market data from bitFlyer, storing candlesticks in SQLite, charting technical indicators, comparing trading strategies, and running automated spot trading.

The default configuration runs **paper trading with live public market data**. The API credentials remain placeholders, so you can start the dashboard without a bitFlyer account. Live automation and the manual order command can place real orders when configured with valid credentials.

The core trading application is based on an online course. I extended it with cost-aware backtesting, chronological validation, and more resilient data ingestion.

The trading model selects technical strategies using historical backtests; it is a rule-based model.

## Contents

- [Requirements](#requirements)
- [Quick start](#quick-start)
- [Configuration](#configuration)
- [Dashboard](#dashboard)
- [Backtesting and automation](#backtesting-and-automation)
- [Command-line modes](#command-line-modes)
- [HTTP API](#http-api)
- [Storage and project structure](#storage-and-project-structure)
- [Development checks](#development-checks)
- [Troubleshooting and limitations](#troubleshooting-and-limitations)

## Requirements

- Go 1.27.2 or later, matching [go.mod](go.mod).
- CGO enabled and a C compiler available for `github.com/mattn/go-sqlite3`.
- Internet access to the bitFlyer public API and WebSocket feed. The browser also loads Google Charts from Google's servers.
- A modern browser with JavaScript enabled for the dashboard.
- Node.js with `node --test` support if you want to run the JavaScript tests.

SQLite is embedded; a separate database server is unnecessary. The frontend is plain JavaScript embedded in the Go executable, with no npm installation or frontend build step.

## Quick start

Run these commands from the repository root:

```sh
go mod download
go run .
```

Open [http://localhost:8080/chart/](http://localhost:8080/chart/). The root URL redirects to the chart. `go run . -mode web` starts the same application explicitly.

The supplied [config.ini](config.ini) uses `BTC_JPY`, one-minute trading decisions, a virtual balance of 1,000,000 JPY, a simulated fee rate of 0.12%, and adverse slippage of 0.05% per fill. Historical research also assumes a full bid/ask spread of 0.05%. These are configurable model assumptions. Leave `back_test = true` and the `X` credential placeholders in place for paper trading.

On startup, the application creates the database tables, resumes the matching automation journal if one exists, starts market-data collection, and serves the dashboard. A fresh database begins empty: candles accumulate from incoming ticks, with no historical download. Indicators and strategy rankings appear as enough data becomes available. After startup or reconnection, automation waits for a full trading-duration candle to be collected, skipping the first partial candle and historical signals.

Stop the application with `Ctrl+C`. Web mode also handles `SIGTERM`. Logging goes to standard output and `gotrading.log` by default.

To build and run an executable:

```sh
mkdir -p bin
go build -o bin/gotrading .
./bin/gotrading
```

Keep running it from the repository root, or provide a `config.ini` in its working directory. Configuration, database, and log paths are resolved from that working directory; the executable location does not determine them.

## Configuration

The application reads `config.ini` at startup. It currently has no `-config` flag or environment-variable overrides. Restart the process to apply configuration changes.

```ini
[bitflyer]
api_key = XXXXXXXXXXX
api_secret = XXXXXXXXXXXXXXX

[gotrading]
log_file = gotrading.log
product_code = BTC_JPY
trade_duration = 1m
back_test = true
use_percent = 0.9
data_limit = 365
stop_limit_percent = 0.9
num_ranking = 3
paper_balance = 1000000
fee_rate = 0.0012
spread_rate = 0.0005
slippage_rate = 0.0005
validation_ratio = 0.3
min_validation_trades = 2

[db]
name = stockdata.sql
driver = sqlite3

[web]
port = 8080
```

| Setting | Default | Meaning |
| --- | --- | --- |
| `bitflyer.api_key` / `api_secret` | `X` placeholders in the supplied file | Required for private account requests and live trading. Public data and paper trading work with placeholders. |
| `gotrading.log_file` | `gotrading.log` | Append-only application log file. |
| `gotrading.product_code` | `BTC_JPY` | Market to collect, display, and trade. Web automation accepts `BTC_JPY`, `BTC_USD`, and `BTC_EUR`; exchange/account availability is separate from this code-level allowlist. |
| `gotrading.trade_duration` | `1m` | Automation timeframe: `1s`, `1m`, or `1h`. All three candle durations are collected regardless of this setting. |
| `gotrading.back_test` | `true` | `true` runs forward paper trading; `false` enables live automation in web mode and requires non-placeholder credentials. This does not control historical API backtests or manual CLI orders. |
| `gotrading.use_percent` | `0.9` | Fraction of available quote-currency balance used for a buy, greater than 0 and at most 1. It is applied to each buy, not a fixed lifetime budget. |
| `gotrading.data_limit` | `365` | Requested candle window for automation and default candle count in the chart; 2–1000. Automation excludes the forming candle, with database reads capped at 1000 rows. |
| `gotrading.stop_limit_percent` | `0.9` | Stop threshold as a fraction of the position's average cost including fees, greater than 0 and at most 1. `0.9` sets it 10% below that cost. |
| `gotrading.num_ranking` | `3` | Maximum number of strategies passing the training/validation profit and trade-count requirements; 1–5. |
| `gotrading.paper_balance` | `1000000` | Initial virtual balance in the pair's quote currency, greater than 0 and at most 10^15. Used only when a new paper journal is created. |
| `gotrading.fee_rate` | `0.0012` | Simulated paper commission and the BTC fee reserve used when sizing live sales; 0–0.1. `0.0012` means 0.12%. Live accounting uses the commission returned by the exchange. |
| `gotrading.spread_rate` | `0.0005` | Full bid/ask spread assumed for historical research; 0–0.1. Half is applied adversely to each side of the midpoint. Paper trading uses the incoming bid/ask instead, without adding this spread again. |
| `gotrading.slippage_rate` | `0.0005` | Adverse price adjustment per historical/paper fill; 0–0.1. Live accounting uses actual reported fill prices. |
| `gotrading.validation_ratio` | `0.3` | Fraction of the latest research window reserved for later chronological validation, greater than 0 and less than 1. Parameters are tuned only on the earlier partition. |
| `gotrading.min_validation_trades` | `2` | Minimum completed validation round trips required to enable a strategy; 1–1000. Both training and validation net profit must also be positive. |
| `db.name` | `stockdata.sql` | SQLite database file. |
| `db.driver` | `sqlite3` | The only supported database driver. |
| `web.port` | `8080` | HTTP port; 1–65535. |

`config.ini` is tracked by Git. Keep committed credentials as placeholders. Account responses and order information may also appear in local logs.

Changing `paper_balance` does not reset an existing balance. Journals are separate for each combination of mode, product, and trading duration. Changing one of those selects another journal; it does not close a position in the previous one.

The strategy-selection policy is stored with each journal. On restart, changed costs, validation rules, analysis window, or ranking count invalidate previously selected parameters so they are selected again. Balances, positions, trade history, and pending orders are retained. Journals created before this policy was introduced receive the same parameter refresh.

Setting `back_test = false` with valid credentials makes a normal web-mode startup eligible to submit real market orders on subsequent signals. There is no separate dashboard activation step. The default server listens on all network interfaces and provides no authentication or TLS, so use it on a trusted local network or behind access controls.

## Dashboard

The interface is in English and uses a simple, left-aligned layout with a 600px chart, native buttons and checkboxes, and compact rows of indicator inputs. The chart fits smaller screens, with a scrollable optimization table. It provides:

- One-second, one-minute, and one-hour candles with a shared time-range filter.
- SMA, EMA, Bollinger Bands, Ichimoku Cloud, estimated volume, RSI, MACD, and historical volatility.
- Individual strategy backtests, trade markers, gross and net profit comparisons, and parameter rankings.
- Separate training/validation results, completed validation trades, selection requirements, and the modeled round-trip break-even price move.
- Automatic-trading records and current mode, position, stop threshold, selected strategies, pending order, and error status.

The browser refreshes roughly every three seconds. Refreshing pauses while the pointer or keyboard focus is inside the chart, and while the page is hidden. It resumes afterward and preserves the selected range where possible. This pauses the display only; backend collection and automation continue while the application runs, including when the browser is closed.

Chart timestamps are displayed in UTC. Changing the displayed duration, indicator periods, or a ranking's displayed parameters affects the chart and its backtest requests. It does not change the automation engine's timeframe or selected parameters.

Candles are built from the midpoint of the best bid and ask. Volume is estimated from positive changes in the ticker's rolling 24-hour product volume. These are locally aggregated quote candles, and the volume estimate is not an exact trade-volume feed. Historical volatility is expressed as a percentage per candle, without annualization. Ichimoku uses periods 9, 26, and 52, with a 26-candle displacement.

## Backtesting and automation

Historical backtests and forward automation share the strategy decision rules, but use different execution and sizing models:

| Behavior | Historical strategy backtest | Paper automation | Live automation |
| --- | --- | --- | --- |
| Data | Stored candle window | New public market ticks and completed candles | New public market ticks and completed candles |
| Fill model | Next candle's midpoint open, adjusted for modeled spread and slippage | Incoming ask/bid, adjusted for adverse slippage | Actual exchange market-order fills |
| Position size | 1 gross BTC per entry; BTC commissions reduce the net holding and exit size | Virtual cash and `use_percent` | Available account balance and `use_percent` for buys; engine-owned BTC for sales |
| Commission | `fee_rate` | `fee_rate` | Actual reported commission |
| Persistence | Returned in the response; not saved as trades | Separate paper journal | Separate live journal |

Realized profit excludes unrealized gains or losses on open positions. Historical backtests include configured costs but do not use the automation engine's stop rule. They do not force a closing trade at the end of a window. API and dashboard research use these configured costs; cost-free Go model helpers remain available as gross-performance baselines. Historical results and validation rankings are not forecasts of future returns.

### Trading costs on short timeframes

Small price moves can be consumed by costs on both entry and exit. More frequent trading repeats those costs more often; changing to a one-minute timeframe does not reduce the cost of a round trip. Under the supplied assumptions, a flat midpoint produces a loss, and the midpoint must rise by about **0.391%** for one modeled buy/sell round trip to break even. This is a model calculation, not an exchange fee quote or a profit target.

For midpoint prices `entry` and `exit`, fee rate `f`, full spread `s`, and per-fill slippage `l`, research uses:

```text
buy fill price  = entry * (1 + s / 2) * (1 + l)
sell fill price = exit  * (1 - s / 2) * (1 - l)
net BTC held    = 1 - f
gross BTC sold  = (1 - f) / (1 + f)
realized profit = gross BTC sold * sell fill price - buy fill price
```

The optimizer and individual backtest display compare gross profit with this net result. They make it possible to identify a strategy whose apparent edge disappears after costs. The model still omits order-book depth, variable liquidity, market impact, and taxes; neither a positive net backtest nor a larger timeframe establishes future profitability.

### Strategy selection

The optimizer splits the requested candle window chronologically. With `validation_ratio = 0.3`, it tunes each strategy's parameters on the earlier 70% using net realized profit, then evaluates the selected parameters on the later 30%. The validation portfolio starts flat. Earlier candles provide indicator warmup; a decision from the last training candle can fill at the first validation open, but a training position is never carried into validation.

A strategy qualifies only when training and validation net profit are both positive and validation contains at least `min_validation_trades` completed round trips. Qualified strategies rank first, ordered by validation net profit; up to `num_ranking` are enabled. Other results remain visible, including losses and strategies with too few trades. No completed validation trades means the result is unavailable.

The validation partition participates in choosing which strategies to enable, so it is not a final blind test. Repeated selection on recent data can still overfit; the split and minimum trade count are checks on the selection process, not evidence of statistical significance.

| Strategy | Optimization search |
| --- | --- |
| EMA crossover | Short period 5–10; long period 12–19 |
| Bollinger Bands | Period 10–19; deviation multiplier 1.9 or 2.0 |
| Ichimoku Cloud | Fixed 9/26/52 periods and 26-candle displacement |
| MACD | Fast period 10–18; slow period 20–29; signal period 5–14 |
| RSI | Period 5–24; buy/sell thresholds fixed at 30/70 |

Automation optimizes when it has no enabled parameters, and selects parameters again after an exit leaves no sellable position. If no strategy qualifies, it waits and tries again on a later bar. The dashboard's optimization request calculates a comparison for display; it does not overwrite the engine's parameters.

### Decision and execution flow

1. Incoming ticks update the SQLite candles for all three durations. A newly opened candle in `trade_duration` notifies the trading worker.
2. The worker uses completed candles only. It skips startup history, already processed bars, partial startup/reconnection candles, stale quotes, and signals whose immediately preceding candle is missing. A gap resets indicator warmup to the most recent continuous run of candles. Stops can still act on a current completed close while strategy history is rebuilding.
3. With no sellable position, at least one buy vote and no sell votes can open a position. With a sellable position, any sell vote or a triggered stop can sell it.
4. The worker saves the pending trade before submitting or simulating an order. Slow order requests do not block candle ingestion; older queued bar notifications are discarded in favor of the latest one.
5. Fills update the position, average cost, realized profit, events, and persistent state. Quantities are rounded down to eight decimal places, and the engine enforces a minimum order size of 0.001 BTC.

The stop is based on the completed candle's close and results in a market sale afterward. It is not a trailing stop or an exchange-hosted protective order, and it cannot execute while the application is offline. A sale is limited to the engine's recorded position and available BTC, with a fee reserve; other account holdings are not intentionally liquidated. A balance too small to sell after reserving fees is treated as dust and may remain.

Paper trading runs locally against public quotes. It does not submit private orders or use an exchange sandbox. It applies commissions and configured adverse slippage to actual incoming bid/ask quotes. Buy sizing uses the adjusted ask so the virtual cash budget is respected. It does not simulate order-book depth, queue priority, or partial fills.

Web mode retries feed failures with delays of 1, 2, 4, 8, 16, and then at most 30 seconds, resetting the delay after a session lasts at least a minute. The dashboard and order-confirmation worker stay running during reconnection. Each session resets its rolling-volume baseline and partial-candle warmup; a timestamp watermark rejects replayed older ticks across sessions. Invalid quotes (including crossed bid/ask), other invalid ticker data, and database failures remain fatal. The standalone `realtime` CLI mode still uses a single connection.

### Order recovery

Live orders are checked approximately every three seconds. Terminal states use the actual executed quantity and commission, including partial fills. After about 80 seconds, the engine attempts to cancel its own accepted order once. An unresolved order after about three minutes halts new trading; known acceptance IDs continue to be checked for reconciliation.

Pending orders survive restarts. If submission fails without a known acceptance ID, the outcome is treated as unknown and the engine halts instead of resending. An empty order-list response is not treated as proof of no execution: the [official bitFlyer API documentation](https://lightning.bitflyer.com/docs?lang=en) states that invalidated orders without executions can be absent from the list. A storage failure or journal version conflict also stops further actions by that worker. Feed reconnection does not retry order submission.

Inspect `pending`, `halted`, and `last_error` through the status endpoint or dashboard. There is currently no UI or API action to reset a halted journal. An ambiguous submission requires reconciliation between exchange orders and local state; restarting alone does not clear it. On shutdown, the worker makes a best-effort cancellation of its own accepted pending order and keeps the journal for later confirmation.

## Command-line modes

```sh
go run . -h
```

| Mode | Command | Behavior |
| --- | --- | --- |
| `web` | `go run .` | Collect candles, run automation, and serve the dashboard and API. |
| `ticker` | `go run . -mode ticker` | Print a single public ticker snapshot. |
| `realtime` | `go run . -mode realtime` | Print public WebSocket ticks until interrupted. |
| `balance` | `go run . -mode balance` | Print private account balances; requires credentials. |
| `orders` | `go run . -mode orders` | Print private child-order history for the configured product; requires credentials. |
| `order` | See below | Submit one real limit order; requires credentials. |

Only `web` mode initializes candle storage and automation. The other modes are standalone API operations.

For the manual order command, set shell variables `LIMIT_PRICE` to the quote-currency price per BTC and `BTC_SIZE` to the quantity in BTC. This command submits a real limit buy:

```sh
go run . -mode order -side BUY -price "$LIMIT_PRICE" -size "$BTC_SIZE"
```

`-side` accepts `BUY` or `SELL` and defaults to `BUY`. `-price` and `-size` must be positive. Manual orders use a one-minute expiry and `GTC` time in force, print the acceptance ID, and exit. They do not use the automation journal or its recovery loop. **`back_test = true` does not simulate the manual `order` command.**

## HTTP API

The following endpoints are available in web mode. The API is read-only; requesting a backtest or optimization does not place an order.

| Endpoint | Purpose |
| --- | --- |
| `GET /chart/` | Dashboard; optional `product_code`, `duration`, and `limit`. |
| `GET /api/candle/` | Candles with optional indicators, events, backtest results, optimization, and automation status. |
| `GET /api/automation/` | Current automation status. |

### Candle requests

```sh
curl 'http://localhost:8080/api/candle/?product_code=BTC_JPY&duration=1m&limit=365'
```

| Parameter | Default | Accepted values |
| --- | --- | --- |
| `product_code` | Required by the candle API | Must match the configured product. |
| `duration` | `1m` | `1s`, `1m`, or `1h`. |
| `limit` | `1000` for the API; `data_limit` for the dashboard | Integer from 1 to 1000. |
| `events` | `false` | `true` or `false`; enables events and a `backtest` summary. |
| `strategy` | `ema` in paper mode, `saved` in live mode | `ema`, `bbands`, `ichimoku`, `macd`, `rsi`, `auto`, or `saved`; used when events are enabled. The dashboard explicitly defaults to `auto`. |
| `optimize` | `false` | `true` or `false`; includes a strategy ranking independently of `events`. |

The response contains `product_code`, `duration`, and `candles`. Candles are the latest requested rows in chronological order, with `time`, `open`, `close`, `high`, `low`, and `volume`. The latest candle may still be forming. An empty database returns `"candles": []`. Historical backtests require a continuous sequence at the requested duration; a missing candle makes that window unavailable for simulation instead of assuming a fill across the gap.

JSON durations are nanoseconds: one minute is `60000000000`. Times use RFC 3339. The API does not synthesize missing candles or fetch older data from the exchange.

### Indicators

Enable each indicator with its query flag, for example:

```sh
curl 'http://localhost:8080/api/candle/?product_code=BTC_JPY&duration=1m&limit=365&ema=true&emaPeriod1=7&emaPeriod2=14&emaPeriod3=50&rsi=true&rsiPeriod=14&macd=true'
```

| Flag | Optional parameters and defaults |
| --- | --- |
| `sma=true` | `smaPeriod1=7`, `smaPeriod2=14`, `smaPeriod3=50` |
| `ema=true` | `emaPeriod1=7`, `emaPeriod2=14`, `emaPeriod3=50` |
| `bbands=true` | `bbandsN=20`, `bbandsK=2` |
| `ichimoku=true` | Fixed periods and displacement; no query overrides. |
| `rsi=true` | `rsiPeriod=14` |
| `macd=true` | `macdPeriod1=12`, `macdPeriod2=26`, `macdPeriod3=9` |
| `hv=true` | `hvPeriod1=21`, `hvPeriod2=63`, `hvPeriod3=252` |

Periods must be integers from 2 to 1000. The Bollinger multiplier must be greater than 0 and at most 10; MACD's fast period must be shorter than its slow period. Volume is already present in every candle and its dashboard toggle requires no extra API flag.

Indicators need sufficient history. Unavailable groups may be omitted, and unavailable values within returned series are JSON `null`. Ichimoku's projected series can extend beyond the candle array; the dashboard adds future display positions without inventing OHLC data.

### Backtests, optimization, and events

Run a historical EMA backtest:

```sh
curl 'http://localhost:8080/api/candle/?product_code=BTC_JPY&duration=1m&limit=365&events=true&strategy=ema&emaPeriod1=7&emaPeriod2=14'
```

Strategies use the same parameter names as the indicator table. EMA uses its first two periods, and its short period must be less than its long period. RSI also accepts `rsiBuy=30` and `rsiSell=70`, with `0 < buy < sell < 100`. Indicator display flags are not required to run a strategy backtest. Costs and validation settings come from `config.ini`; the API has no per-request overrides for them. Individual historical results include `backtest.costs` and cost-free `backtest.gross_profit` alongside the net `profit`.

Request the recorded automation events and a separate optimization comparison:

```sh
curl 'http://localhost:8080/api/candle/?product_code=BTC_JPY&duration=1m&limit=365&events=true&strategy=auto&optimize=true'
```

`events` contains `signals`, `profit`, `completed_trades`, and `holding`. The `backtest` object identifies the source and execution model:

| Strategy source | Execution value | Scope |
| --- | --- | --- |
| `ema`, `bbands`, `ichimoku`, `macd`, `rsi` | `next_open` | Historical simulation over the requested candle window. |
| `auto` | `recorded_fills` | All events in the active mode/product/trading-duration journal. Profit is cumulative for that journal, regardless of chart window. |
| `saved` | `stored` | Legacy `signal_events` records in the requested time window, retaining an earlier opening buy when needed for cost basis. |

Automation stores its events in its own journal, so `saved` does not show the new automation records. Costed historical and automation events use commission-adjusted `price` and `size` for profit accounting; `execution_price` retains the modeled or exchange-reported fill price, and `commission` is recorded in BTC. Each settled sale contributes to the automation sell count, including partial sales. Chart markers use execution prices and group events within the displayed candle.

Optimization adds `rankings`, selected `parameters`, `best_strategy`, `candle_count`, and `as_of` under `optimization`. It also includes the `policy`, `training_count`, `validation_count`, `validation_start`, and `break_even_rise` as a fractional rate. Each ranking reports `training_profit`, `training_trades`, validation net `performance`, cost-free validation `gross_performance`, and validation `completed_trades`. It runs synchronously for that request. The dashboard requests it only when the optimization button is used, rather than on every refresh.

### Automation status and errors

```sh
curl 'http://localhost:8080/api/automation/'
```

The status includes `mode` (`paper` or `live`), `product_code`, `duration`, `fee_rate`, `quantity`, `stop_limit`, `realized_profit`, selected `params`, `policy`, `optimized_at`, `pending`, `halted`, and `last_error`. The policy records research costs and selection requirements. `cash` is the simulated cash balance in paper mode; it is not a live account-balance snapshot. `last_bar` is the processed-bar/startup cutoff, not the time of the last fill. The candle response also includes `automation` once the engine is available.

API errors use an HTTP status and a JSON object with `error` and `code`. Invalid candle parameters return `400`, unsupported methods return `405`, unknown API paths return `404`, and database/encoding errors return `500`. The standalone automation endpoint returns `503` if the engine has not started.

## Storage and project structure

Web mode creates SQLite tables automatically and uses WAL mode with a busy timeout. Candle tables are separate for each product and duration, such as `BTC_JPY_1m0s`. `signal_events` holds legacy events. `automation_state` holds versioned JSON journals keyed by mode, product, and trading duration, including balances, positions, selected parameters, evaluation policy, pending orders, and events. The added policy is part of the existing JSON payload; no destructive database reset is needed.

An existing paper journal resumes its cash and position instead of receiving a new initial balance. Keep the database when restarting; a new or deleted database loses the engine's knowledge of its previous orders and holdings. For a separate paper experiment, choose a different database filename and keep `back_test = true`.

```text
.
├── main.go                 # CLI and web-process lifecycle
├── config.ini              # Runtime settings and credential placeholders
├── config/                 # INI loading and validation
├── bitflyer/               # Signed HTTP requests and public WebSocket client
├── app/
│   ├── controllers/        # Ingestion, automation, HTTP handlers, query validation
│   ├── models/             # SQLite storage, indicators, events, and backtests
│   └── views/              # Embedded dashboard template and JavaScript
├── tradingalgo/            # Ichimoku, MACD, and historical-volatility calculations
├── tests/                  # JavaScript chart tests
└── utils/                  # Logging setup
```

The default SQLite file and its WAL/SHM sidecars are Git-ignored. Custom database filenames and log files are not covered by a general ignore rule, so check them before committing.

## Development checks

Run from the repository root:

```sh
go test -race ./...
go vet ./...
node --test tests/chart.test.cjs
go build ./...
```

Go tests cover API authentication and cancellation, candle ingestion/storage, reconnect backoff and replay rejection, indicators, gross/net execution costs, training/validation isolation, historical strategy behavior, optimization, automation accounting, partial fills, persistence, and order recovery. Private exchange operations use mocks. JavaScript tests use browser/chart stubs and cover polling, request cancellation, range preservation, indicators, events, validation/cost rankings, and automation status; they do not replace a real-browser check.

These commands do not place live orders. They do not require real credentials. The JavaScript tests use Node's built-in test runner and require no npm packages. Live-account behavior still needs separate validation before operational use.

## Troubleshooting and limitations

| Symptom | What to check |
| --- | --- |
| `config.ini` cannot be loaded | Run from a directory containing that file. There is no automatic config-file discovery. |
| SQLite reports a CGO/compiler error | Check `go env CGO_ENABLED` and ensure a C compiler is installed; this project uses the CGO SQLite driver. |
| Port 8080 is occupied | Change `[web] port` and restart, then use the new port in the browser and API requests. |
| The chart is empty | Check connectivity and logs. Data starts accumulating from incoming ticks; there is no historical backfill. |
| Indicators or rankings are missing | Let enough continuous candles accumulate for indicator warmup, training, and validation. Ranking availability needs completed validation trades; adoption also needs the configured minimum count and positive net profit in both partitions. Long timeframes need longer collection. |
| Chart updates pause | Move the pointer/focus outside the chart and make the tab visible. Backend collection and trading continue. |
| The chart library does not load | Allow the Google Charts scripts in the browser and check network access. The frontend is not fully offline. |
| Private modes reject credentials | Placeholders work only for public requests and paper mode. Private requests need credentials and appropriate account permissions. |
| No automated trades occur | Check the current bar, selected strategies, available balance, minimum size, `pending`, `halted`, and `last_error`. A positive ranking alone does not imply a current buy signal. |
| Candles stop advancing after a connection failure | Web mode reconnects automatically; inspect `action=reconnect_ticker` logs. Cached data may remain visible during the interruption. Trading waits for a full collected candle afterward, and missing history may require indicator warmup. |
| The application exits after a data or storage error | Inspect the logs for invalid ticker data, database errors, or server failures. These remain fatal instead of being hidden by network retries. The standalone `realtime` CLI also exits when its stream ends. |
| An order outcome is unknown or the journal is halted | Reconcile exchange orders and local state. Do not clear the journal merely to force another submission. See [Order recovery](#order-recovery). |

The application currently handles one configured product per process. Automated trading is limited to the BTC spot pairs accepted by the code, with a hard-coded minimum order size; it does not discover exchange product rules dynamically. Its research backtests, local paper execution, and live fills have the different assumptions described above. Validation is one chronological split, not a walk-forward evaluation or an independent final test. Exchange-hosted protective stops, historical backfill, and order-book execution simulation remain outside the current implementation.
