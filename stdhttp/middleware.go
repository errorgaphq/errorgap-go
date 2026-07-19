// Package stdhttp provides net/http error, request-context, and APM
// instrumentation for Errorgap.
package stdhttp

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	errorgap "github.com/errorgaphq/errorgap-go"
)

// Recover wraps a handler with panic reporting and optional APM transaction
// collection. It writes a 500 response after a panic.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		route := &routeState{}
		ctx := context.WithValue(r.Context(), routeStateKey{}, route)
		ctx, collector := errorgap.WithSpanCollector(ctx)
		r = r.WithContext(ctx)
		writer := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			rec := recover()
			status := writer.status
			if rec != nil {
				status = http.StatusInternalServerError
				err, ok := rec.(error)
				if !ok {
					err = fmt.Errorf("%v", rec)
				}
				route := normalizedRoute(r)
				errorgap.Notify(err, errorgap.NoticeOptions{
					Context: map[string]any{
						"source": "stdhttp", "url": requestURL(r),
						"component": route, "action": r.Method,
					},
					Environment: map[string]any{
						"method": r.Method, "path": r.URL.Path, "route": route,
						"user_agent": r.UserAgent(), "remote_addr": r.RemoteAddr,
					},
					Session:       map[string]any{"request_id": r.Header.Get("x-request-id")},
					Params:        requestParams(r),
					BacktraceSkip: 1,
				})
				if !writer.wroteHeader {
					writer.WriteHeader(status)
				}
			}
			errorgap.NotifyTransaction(errorgap.Transaction{
				Kind: "web", Method: r.Method, Path: normalizedRoute(r), PathRaw: r.URL.Path,
				StatusCode: status, DurationMS: float64(time.Since(started)) / float64(time.Millisecond),
				OccurredAt: started.UTC(), Spans: collector.Spans(),
			})
		}()
		next.ServeHTTP(writer, r)
	})
}

type routeStateKey struct{}
type routeState struct{ pattern string }

// Route annotates a handler with its normalized path pattern. It is useful on
// Go 1.22; newer Go releases expose ServeMux's matched pattern automatically.
func Route(pattern string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if state, _ := r.Context().Value(routeStateKey{}).(*routeState); state != nil {
			state.pattern = pattern
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func normalizedRoute(r *http.Request) string {
	pattern := ""
	if state, _ := r.Context().Value(routeStateKey{}).(*routeState); state != nil {
		pattern = state.pattern
	}
	if pattern == "" {
		// Request.Pattern was added after Go 1.22. Reflection preserves
		// source compatibility with the SDK's minimum supported version.
		value := reflect.ValueOf(r)
		if value.Kind() == reflect.Pointer && !value.IsNil() {
			field := value.Elem().FieldByName("Pattern")
			if field.IsValid() && field.Kind() == reflect.String {
				pattern = field.String()
			}
		}
	}
	pattern = strings.TrimSpace(pattern)
	if _, route, ok := strings.Cut(pattern, " "); ok {
		pattern = route
	}
	if pattern == "" {
		return r.URL.Path
	}
	return pattern
}

func requestURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + r.URL.Path
}

func requestParams(r *http.Request) map[string]any {
	params := map[string]any{}
	query := map[string]any{}
	for key, values := range r.URL.Query() {
		if len(values) == 1 {
			query[key] = values[0]
		} else {
			query[key] = values
		}
	}
	params["query"] = query
	if err := r.ParseForm(); err == nil && len(r.PostForm) > 0 {
		form := map[string]any{}
		for key, values := range r.PostForm {
			if len(values) == 1 {
				form[key] = values[0]
			} else {
				form[key] = values
			}
		}
		params["form"] = form
	}
	return params
}
