// Package errorgapgin reports Gin panics and handler errors to Errorgap and
// records each request as an APM transaction named by its route
// (`/orders/:id`).
//
//	r := gin.New()
//	r.Use(gin.Recovery(), errorgapgin.Middleware())
//
// Register it after gin.Recovery: a panic is reported here and then
// re-panicked, so Recovery still writes the 500. Inside handlers, report
// with errorgap.NotifyContext(c.Request.Context(), err), or attach the error
// with c.Error(err) and it is reported when the response is a 5xx.
package errorgapgin

import (
	"fmt"
	"net/http"
	"time"

	errorgap "github.com/errorgaphq/errorgap-go"
	"github.com/gin-gonic/gin"
)

// Middleware returns the Gin middleware.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		transactionID := errorgap.NewTransactionID()
		ctx := errorgap.WithTransactionID(c.Request.Context(), transactionID)
		ctx, collector := errorgap.WithSpanCollector(ctx)
		c.Request = c.Request.WithContext(ctx)

		defer func() {
			recovered := recover()
			status := c.Writer.Status()
			route := c.FullPath()
			if route == "" {
				route = c.Request.URL.Path
			}
			options := func() errorgap.NoticeOptions {
				return errorgap.NoticeOptions{
					Context: map[string]any{
						"source": "gin", "component": route, "action": c.Request.Method,
					},
					Environment: map[string]any{
						"method": c.Request.Method, "path": c.Request.URL.Path, "route": route,
						"user_agent": c.Request.UserAgent(), "remote_addr": c.ClientIP(),
					},
					Params:        map[string]any{"query": query(c.Request)},
					BacktraceSkip: 1,
					TransactionID: transactionID,
				}
			}
			if recovered != nil {
				status = http.StatusInternalServerError
				err, ok := recovered.(error)
				if !ok {
					err = fmt.Errorf("%v", recovered)
				}
				errorgap.Notify(err, options())
			} else if status >= http.StatusInternalServerError {
				for _, attached := range c.Errors {
					errorgap.Notify(attached.Err, options())
				}
			}
			errorgap.NotifyTransaction(errorgap.Transaction{
				ID:      transactionID,
				TraceID: errorgap.BrowserTraceID(c.Request.Header.Get(errorgap.TraceHeader)),
				Kind:    "web", Method: c.Request.Method, Path: route, PathRaw: c.Request.URL.Path,
				StatusCode: status, DurationMS: float64(time.Since(started)) / float64(time.Millisecond),
				OccurredAt: started.UTC(), Spans: collector.Spans(),
			})
			if recovered != nil {
				panic(recovered)
			}
		}()
		c.Next()
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
