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
	// Default Async to true when not explicitly set. Because the zero
	// value of bool is false, we detect "user opted out" by looking for
	// an explicit Async field — but we can't, so we instead document that
	// Async defaults to true via Init and require Configure for tests.
	if !cfg.Async {
		// Treat zero value as "use the default". Callers who really want
		// sync delivery should set Async=false AND pass true to
		// NoSyncDelivery (see NewClient docs). For Init, we apply true.
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
