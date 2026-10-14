// Package errorgapfiber reports Fiber panics and handler errors to Errorgap
// and records each request as an APM transaction named by its route
// (`/orders/:id`).
//
//	app := fiber.New()
//	app.Use(recover.New(), errorgapfiber.Middleware())
//
// Register it after Fiber's recover middleware: a panic is reported here and
// then re-panicked, so recover still turns it into a 500. An error a handler
// returns is reported when it is not a *fiber.Error below 500. Inside
// handlers, report with errorgap.NotifyContext(c.UserContext(), err).
package errorgapfiber

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	errorgap "github.com/errorgaphq/errorgap-go"
	"github.com/gofiber/fiber/v2"
)

// Middleware returns the Fiber middleware.
func Middleware() fiber.Handler {
	return func(c *fiber.Ctx) (err error) {
		started := time.Now()
		transactionID := errorgap.NewTransactionID()
		ctx := errorgap.WithTransactionID(c.UserContext(), transactionID)
		ctx, collector := errorgap.WithSpanCollector(ctx)
		c.SetUserContext(ctx)
		// Fiber reuses these buffers after the handler returns: copy now.
		method := string([]byte(c.Method()))
		path := string([]byte(c.Path()))

		defer func() {
			recovered := recover()
			status := c.Response().StatusCode()
			route := path
			if matched := c.Route(); matched != nil && matched.Path != "" && matched.Path != "/" {
				route = matched.Path
			}
			failure := err
			if recovered != nil {
				status = http.StatusInternalServerError
				var ok bool
				if failure, ok = recovered.(error); !ok {
					failure = fmt.Errorf("%v", recovered)
				}
			} else if err != nil {
				// Fiber's error handler writes the response after this
				// middleware: work out the status it will use.
				var fiberError *fiber.Error
				if errors.As(err, &fiberError) {
					status = fiberError.Code
				} else {
					status = http.StatusInternalServerError
				}
			}
			if failure != nil && status >= http.StatusInternalServerError {
				query := map[string]any{}
				c.Context().QueryArgs().VisitAll(func(key, value []byte) {
					query[string(key)] = string(value)
				})
				errorgap.Notify(failure, errorgap.NoticeOptions{
					Context: map[string]any{"source": "fiber", "component": route, "action": method},
					Environment: map[string]any{
						"method": method, "path": path, "route": route,
						"user_agent": string(c.Request().Header.UserAgent()), "remote_addr": c.IP(),
					},
					Params:        map[string]any{"query": query},
					BacktraceSkip: 1,
					TransactionID: transactionID,
				})
			}
			errorgap.NotifyTransaction(errorgap.Transaction{
				ID:      transactionID,
				TraceID: errorgap.BrowserTraceID(c.Get(errorgap.TraceHeader)),
				Kind:    "web", Method: method, Path: route, PathRaw: path,
				StatusCode: status, DurationMS: float64(time.Since(started)) / float64(time.Millisecond),
				OccurredAt: started.UTC(), Spans: collector.Spans(),
			})
			if recovered != nil {
				panic(recovered)
			}
		}()
		return c.Next()
	}
}
