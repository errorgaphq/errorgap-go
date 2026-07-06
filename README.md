# errorgap-go

Go notifier for [Errorgap](https://errorgap.com). Captures errors and
panics, walks the goroutine stack, and ships notices to an Errorgap
server. Ships a net/http recovery middleware; gin/chi/echo/fiber adapters
will follow in 0.2.

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

## net/http

```go
mux := http.NewServeMux()
mux.HandleFunc("/", handler)
http.ListenAndServe(":8080", stdhttp.Recover(mux))
```

The middleware catches panics, reports them, and returns a 500 response.

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
| `Async` | `true` (via `Init`) | Background goroutine delivery |
| `Logger` | `slog.New(...io.Discard)` | Replace to surface SDK warnings |
| `FilterKeys` | `["password", "token", ...]` | Substring, case-insensitive |
| `HTTPClient` | `&http.Client{Timeout: 5s}` | Plug in your own transport |
| `QueueSize` | `100` | Drops oldest notice when full |

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
