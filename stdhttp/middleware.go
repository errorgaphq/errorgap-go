// Package stdhttp provides a net/http middleware that reports panics to
// Errorgap.
package stdhttp

import (
	"fmt"
	"net/http"

	errorgap "github.com/errorgaphq/errorgap-go"
)

// Recover wraps a handler with a panic-recovering middleware that reports
// the panic to Errorgap and writes a 500 response to the client.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			err, ok := rec.(error)
			if !ok {
				err = fmt.Errorf("%v", rec)
			}
			errorgap.Notify(err, errorgap.NoticeOptions{
				Context: map[string]any{
					"source":    "stdhttp",
					"url":       r.URL.String(),
					"component": r.URL.Path,
					"action":    r.Method,
				},
				Environment: map[string]any{
					"method":     r.Method,
					"path":       r.URL.Path,
					"user_agent": r.Header.Get("user-agent"),
					"remote_addr": r.RemoteAddr,
				},
			})
			w.WriteHeader(http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
