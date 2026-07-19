// Package errorgap is the Go notifier for the Errorgap error-tracking
// platform. Use Init to configure the package-level default client and
// Notify / Flush / Close as the simple, package-level entry points.
//
// For libraries or apps that want isolated state (e.g. tests), instantiate
// a Client directly with NewClient.
package errorgap

import (
	"context"
	"errors"
	"os"
	"sync"
)

var (
	defaultMu     sync.RWMutex
	defaultClient *Client
)

// Init configures the package-level default client. Subsequent calls
// replace the existing default. The previous client is closed in the
// background so in-flight deliveries still finish.
func Init(cfg Config) error {
	// Default Async to true because bool's zero value cannot distinguish an
	// omitted setting. ERRORGAP_ASYNC=false provides an explicit synchronous
	// package-level mode; callers can also use NewClient with Async false.
	if value, ok := os.LookupEnv("ERRORGAP_ASYNC"); ok {
		cfg.Async = parseBool(value, cfg.Async)
	} else if !cfg.Async {
		// Treat the zero value as "use the package default".
		cfg.Async = true
	}
	c, err := NewClient(cfg)
	if err != nil {
		return err
	}
	defaultMu.Lock()
	old := defaultClient
	defaultClient = c
	defaultMu.Unlock()
	if old != nil {
		go func() {
			_ = old.Close(context.Background())
		}()
	}
	return nil
}

// NotifyTransaction sends an APM transaction via the package-level client.
func NotifyTransaction(transaction Transaction) Result {
	c := getDefault()
	if c == nil {
		return Result{Err: errors.New("errorgap: not initialized")}
	}
	return c.NotifyTransaction(transaction)
}

// NotifyLog sends a structured log event via the package-level client.
func NotifyLog(message, level, source string) Result {
	c := getDefault()
	if c == nil {
		return Result{Err: errors.New("errorgap: not initialized")}
	}
	return c.NotifyLog(message, level, source)
}

// TrackJob runs operation as a background job using the package-level client.
func TrackJob(ctx context.Context, jobClass, queue string, operation func(context.Context) error) error {
	c := getDefault()
	if c == nil {
		return errors.New("errorgap: not initialized")
	}
	return c.TrackJob(ctx, jobClass, queue, operation)
}

// Notify sends an error via the package-level client. Returns an empty
// Result if Init has not been called.
func Notify(err error, opts ...NoticeOptions) Result {
	c := getDefault()
	if c == nil {
		return Result{Err: errors.New("errorgap: not initialized")}
	}
	return c.Notify(err, opts...)
}

// Flush blocks until in-flight async deliveries finish.
func Flush(ctx context.Context) error {
	c := getDefault()
	if c == nil {
		return nil
	}
	return c.Flush(ctx)
}

// Close drains and shuts down the package-level client.
func Close(ctx context.Context) error {
	defaultMu.Lock()
	c := defaultClient
	defaultClient = nil
	defaultMu.Unlock()
	if c == nil {
		return nil
	}
	return c.Close(ctx)
}

// Recover wraps a deferred panic recovery: any non-nil recovered value is
// reported to Errorgap, then re-panicked so caller stacks observe it.
//
//	defer errorgap.Recover()
func Recover() {
	if r := recover(); r != nil {
		err, ok := r.(error)
		if !ok {
			err = errors.New(toString(r))
		}
		Notify(err, NoticeOptions{Context: map[string]any{"source": "panic"}})
		panic(r)
	}
}

func getDefault() *Client {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultClient
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "panic"
}
