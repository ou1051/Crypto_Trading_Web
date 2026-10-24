# セクション15：Bitcoinを取り扱うAPIの実装

参照元：`gotrading_application.zip` の講義97〜101。
講義96はAPIの説明です。セクション16以降の実装は含めていません。

## 追加した機能

| 講義 | 実装 |
| --- | --- |
| 97 | 既存のAPI認証・`GetBalance`を維持 |
| 98 | `Ticker`、`GetTicker`、中間価格・日時取得・秒／分／時間への切り捨て |
| 99・100 | `GetRealTimeTicker`によるリアルタイム配信（WebSocket方式） |
| 101 | `Order`、`SendOrder`、`ListOrder`による売買注文・注文一覧取得 |

`config/config.go` に `ProductCode` を追加し、`config.ini` に教材と同じ
`product_code = BTC_USD` を追加しました。APIキー・シークレットは元の値を維持しています。

## 教材から変更した箇所と理由

- **`bitflyer/bitflyer.go`：PubNub方式を追加せず、WebSocket方式を採用。**
  教材の講義100でもPubNub版は廃止予定のコメントとともに無効になっています。
  現在の[公式Realtime API仕様](https://bf-lightning-api.readme.io/docs/endpoint-json-rpc)に沿って
  `wss://ws.lightstream.bitflyer.com/json-rpc` を使用します。
- **`bitflyer/bitflyer.go`：`ioutil.ReadAll` を `io.ReadAll` に変更。**
  古い `io/ioutil` の代わりに現在の標準ライブラリの書き方を使います。
- **`bitflyer/bitflyer.go`：Public APIには認証ヘッダを付けない。**
  公開Tickerは認証が不要なので、`XXXXX` のままでも取得できます。
- **`bitflyer/bitflyer.go`：タイムゾーン表記のない日時もUTCとして解析。**
  [公式HTTP API仕様](https://lightning.bitflyer.com/docs?lang=ja)のTicker例は
  `Z` のないUTC時刻です。WebSocketで使われる `Z` 付き・小数秒付きの日時にも対応します。
- **`bitflyer/bitflyer.go`：JSON-RPCの受信に `json.RawMessage` を使用。**
  教材の `interface{}` と型判定・JSON再生成の代わりに、通知の内容を直接解析します。
  購読の成功・失敗も確認してエラーを返します。
- **`bitflyer/bitflyer.go`：リアルタイム配信のエラーを呼び出し元へ返す。**
  接続失敗時に `log.Fatal` でプロセス全体を終了させず、終了時にはTickerのチャネルを閉じます。
  追加の `GetRealTimeTickerContext` は停止指示で接続・受信待ち・送信待ちを解除できます。
  切断後の自動再接続は実装していません。
- **`bitflyer/bitflyer.go`：注文送信時にはリクエストに必要な項目だけをJSON化。**
  注文状態・約定数量など、取得結果にしか使わないフィールドを送信しないためです。
- **`main.go`：講義ごとの実行例を `-mode` で選択。**
  教材の最後の状態は起動すると固定価格で買い注文を送ります。
  APIキーがない現在の環境で動作を確認できるよう、通常起動は公開Ticker取得にしています。
  注文は `-mode order` で価格・数量・売買方向を指定します。
  `XXXXX` のままPrivate APIを選んだ場合は、通信せず理由を表示して異常終了します。
  教材で無視していたエラーも確認し、失敗時のnil参照を防ぎます。
- **`config/config.go`：`log.Fatalf` の直後の `os.Exit(1)` を削除。**
  `log.Fatalf` 自体が終了するため、重複した処理を取り除きました。
- **`go.mod`・`go.sum`：`github.com/gorilla/websocket v1.5.3` を追加。**
  教材と同じWebSocketライブラリをGo Modulesで管理します。

以前修正した `/v1` を含むURL・HTTPステータス確認・15秒のHTTPタイムアウトは維持しています。

## 実行方法

プロジェクトのルートで実行します。

```sh
go run .
go run . -mode realtime
```

通常起動はTickerを1回取得します。リアルタイム配信は `Ctrl+C` で終了します。

実際のAPIキーが必要な処理は `-mode balance`（残高）、`-mode orders`（注文一覧）、
`-mode order -side BUY -price <価格> -size <数量>`（指値買い注文）です。
指値売り注文は `-side SELL` を使います。注文の有効期間は教材と同じ1分です。
APIキー・シークレットが `XXXXX` の間はこれらを実行できません。

## 検証

```sh
go test -race ./...
go vet ./...
```

`bitflyer/bitflyer_test.go` では、モックHTTP通信とローカルWebSocketサーバーを使って、
APIパス・クエリ・署名・認証なしのTicker取得・日時解析・売買注文・HTTPエラー・
リアルタイム購読・購読失敗・キャンセルを確認します。テストは実際の注文を送信しません。
