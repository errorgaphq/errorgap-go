// Package errorgapecho reports Echo panics and handler errors to Errorgap
// and records each request as an APM transaction named by its route
// (`/orders/:id`).
//
//	e := echo.New()
//	e.Use(middleware.Recover(), errorgapecho.Middleware())
//
// Register it after Echo's Recover: a panic is reported here and then
// re-panicked, so Recover still turns it into a 500. An error a handler
// returns is reported when it is not an *echo.HTTPError below 500.
package errorgapecho

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	errorgap "github.com/errorgaphq/errorgap-go"
	"github.com/labstack/echo/v4"
)

// Middleware returns the Echo middleware.
func Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			started := time.Now()
			request := c.Request()
			transactionID := errorgap.NewTransactionID()
			ctx := errorgap.WithTransactionID(request.Context(), transactionID)
			ctx, collector := errorgap.WithSpanCollector(ctx)
			c.SetRequest(request.WithContext(ctx))

			defer func() {
				recovered := recover()
				status := c.Response().Status
				route := c.Path()
				if route == "" {
					route = request.URL.Path
				}
				failure := err
				if recovered != nil {
					status = http.StatusInternalServerError
					var ok bool
					if failure, ok = recovered.(error); !ok {
						failure = fmt.Errorf("%v", recovered)
					}
				} else if err != nil {
					// Echo writes the response for a returned error after
					// this middleware: work out the status it will use.
					var httpError *echo.HTTPError
					if errors.As(err, &httpError) {
						status = httpError.Code
					} else {
						status = http.StatusInternalServerError
					}
				}
				if failure != nil && status >= http.StatusInternalServerError {
					errorgap.Notify(failure, errorgap.NoticeOptions{
						Context: map[string]any{
							"source": "echo", "component": route, "action": request.Method,
						},
						Environment: map[string]any{
							"method": request.Method, "path": request.URL.Path, "route": route,
							"user_agent": request.UserAgent(), "remote_addr": c.RealIP(),
						},
						Params:        map[string]any{"query": query(request)},
						BacktraceSkip: 1,
						TransactionID: transactionID,
					})
				}
				errorgap.NotifyTransaction(errorgap.Transaction{
					ID:      transactionID,
					TraceID: errorgap.BrowserTraceID(request.Header.Get(errorgap.TraceHeader)),
					Kind:    "web", Method: request.Method, Path: route, PathRaw: request.URL.Path,
					StatusCode: status, DurationMS: float64(time.Since(started)) / float64(time.Millisecond),
					OccurredAt: started.UTC(), Spans: collector.Spans(),
				})
				if recovered != nil {
					panic(recovered)
				}
			}()
			return next(c)
		}
	}
}

func query(r *http.Request) map[string]any {
	out := map[string]any{}
	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			out[key] = values[0]
		} else {
			out[key] = values
		}
	}
	return out
}
