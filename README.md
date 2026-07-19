# errorgap-go

Go notifier for [Errorgap](https://errorgap.com). Captures errors and panics,
embeds application and dependency source excerpts, instruments `net/http`
requests and background jobs for APM, and forwards standard-library `slog`
records.

## Install

```sh
go get github.com/errorgaphq/errorgap-go
```

Requires Go 1.22+.

## Configure

```go
package main

import (
    "context"
    "os"

    errorgap "github.com/errorgaphq/errorgap-go"
)

func main() {
    err := errorgap.Init(errorgap.Config{
        Endpoint:    os.Getenv("ERRORGAP_ENDPOINT"),
        ProjectSlug: os.Getenv("ERRORGAP_PROJECT_SLUG"),
        APIKey:      os.Getenv("ERRORGAP_API_KEY"),
        Environment: os.Getenv("APP_ENV"),
        APMEnabled:  true,
        LogsEnabled: true,
    })
    if err != nil {
        panic(err)
    }
    defer errorgap.Close(context.Background())
    // ... your app ...
}
```

`Init` reads the same values from `ERRORGAP_ENDPOINT`,
`ERRORGAP_PROJECT_SLUG`, `ERRORGAP_PROJECT_ID`, and `ERRORGAP_API_KEY` if
you leave them empty.

## Manual notification

```go
if err := risky(); err != nil {
    errorgap.Notify(err, errorgap.NoticeOptions{
        Context: map[string]any{"component": "billing"},
    })
    return err
}
```

`Notify` returns a `Result` (`{Status, Body, Err, Queued}`). The SDK never
panics — recoverable failures are logged via the configured `slog.Logger`.

## net/http errors and APM

```go
mux := http.NewServeMux()
mux.Handle("GET /orders/{orderId}",
    stdhttp.Route("/orders/{orderId}", http.HandlerFunc(handler)))
http.ListenAndServe(":8080", stdhttp.Recover(mux))
```

The middleware catches panics, reports them, returns a 500 response, and sends
request duration, status, raw path, and normalized route statistics. On Go
versions that expose the matched `ServeMux` pattern, `stdhttp.Route` is
optional; keeping it makes normalized paths work on Go 1.22 too.

Record database and outbound HTTP work against the request context:

```go
started := time.Now()
rows, err := db.QueryContext(r.Context(), "SELECT * FROM orders WHERE id = ?", id)
errorgap.RecordDatabase(r.Context(), "SELECT * FROM orders WHERE id = 42", time.Since(started))

started = time.Now()
response, err := http.DefaultClient.Do(request)
errorgap.RecordExternal(r.Context(), time.Since(started))
```

SQL literals are normalized before delivery so equivalent queries aggregate.

## Background jobs

```go
err := errorgap.TrackJob(ctx, "ReceiptJob", "critical", func(ctx context.Context) error {
    errorgap.RecordDatabase(ctx, "SELECT 42 AS receipt", 5*time.Millisecond)
    return generateReceipt(ctx)
})
```

Failed jobs send both an error notice and a failed job transaction.

## slog forwarding

```go
handler := errorgap.NewSlogHandler(slog.NewJSONHandler(os.Stdout, nil), nil, slog.LevelWarn)
logger := slog.New(handler)
logger.Warn("payment gateway timeout", "order_id", orderID)
```

Pass a `*Client` instead of `nil` when not using the package-level client.

## Recover helper

For background goroutines or workers without HTTP middleware:

```go
func worker() {
    defer errorgap.Recover()
    // ... risky code that may panic ...
}
```

`Recover` reports the panic and re-panics so the host's normal panic
handling still runs.

## Graceful shutdown

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
_ = errorgap.Flush(ctx) // wait for queued notices
_ = errorgap.Close(ctx) // shut down the worker goroutine
```

## Configuration reference

| Field | Default | Notes |
|---|---|---|
| `Endpoint` | `ERRORGAP_ENDPOINT` or `http://127.0.0.1:3030` | Base URL, no trailing slash |
| `ProjectSlug` | `ERRORGAP_PROJECT_SLUG` | **Required** |
| `ProjectID` | `ERRORGAP_PROJECT_ID` | Optional, embedded in payload |
| `APIKey` | `ERRORGAP_API_KEY` | Sent as `x-errorgap-project-key` |
| `Environment` | `ERRORGAP_ENVIRONMENT` or `"production"` | |
| `Release` | — | Embedded in `context.release` |
| `RootDirectory` | current directory | Classifies app frames and makes their paths relative |
| `Async` | `true` (via `Init`) | Background goroutine delivery |
| `Logger` | discard logger | Replace to surface SDK diagnostics |
| `FilterKeys` | `["password", "token", ...]` | Substring, case-insensitive |
| `HTTPClient` | `&http.Client{Timeout: 5s}` | Plug in your own transport |
| `QueueSize` | `100` | Drops the new telemetry item when full |
| `APMEnabled` | `ERRORGAP_APM_ENABLED` or `false` | Sends requests, spans, and jobs |
| `APMSampleRate` | `ERRORGAP_APM_SAMPLE_RATE` or `1` | Fraction from 0 to 1 |
| `LogsEnabled` | `ERRORGAP_LOGS_ENABLED` or `false` | Enables `NotifyLog` and `SlogHandler` delivery |
| `MinimumLogLevel` | `ERRORGAP_MINIMUM_LOG_LEVEL` or `WARN` | Threshold used by `NewSlogHandler` |

## Verify

```sh
curl -sS -X POST "$ERRORGAP_ENDPOINT/api/projects/$ERRORGAP_PROJECT_SLUG/notices" \
  -H "content-type: application/json" \
  -H "x-errorgap-project-key: $ERRORGAP_API_KEY" \
  -d '{"errors":[{"type":"ErrorgapInstallTest","message":"Errorgap install verification"}],"context":{"environment":"development"}}'
```

Then trigger a real error and confirm it appears in the Errorgap UI.

## Development

```sh
go test ./...
```

## License

MIT.
