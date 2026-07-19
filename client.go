package errorgap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Result records the outcome of a single Notify call.
type Result struct {
	Status int
	Body   []byte
	Err    error
	Queued bool
}

// Success reports whether delivery returned a 2xx.
func (r Result) Success() bool {
	return r.Err == nil && r.Status >= 200 && r.Status < 300
}

// Client posts notices to an Errorgap server. Safe for concurrent use.
type Client struct {
	mu       sync.RWMutex
	cfg      Config
	queue    chan delivery
	wg       sync.WaitGroup
	stopOnce sync.Once
	inFlight atomic.Int64
}

type delivery struct {
	resource string
	payload  any
}

// NewClient validates the config, applies defaults, and starts the async
// delivery goroutine. The caller should defer Close() to flush in-flight
// deliveries during shutdown.
func NewClient(cfg Config) (*Client, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, queue: make(chan delivery, cfg.QueueSize)}
	if cfg.Async {
		c.wg.Add(1)
		go c.loop()
	}
	return c, nil
}

// Config returns a copy of the client's effective configuration.
func (c *Client) Config() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg
}

// Notify queues an error for delivery and returns immediately when Async is
// true. When Async is false, it blocks until the HTTP call completes.
func (c *Client) Notify(err error, opts ...NoticeOptions) Result {
	if err == nil {
		return Result{}
	}
	var o NoticeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()

	notice := buildNotice(err, &cfg, o)
	return c.submit(delivery{resource: "notices", payload: notice})
}

// NotifyTransaction sends an APM transaction when APM is enabled and the
// configured sample rate accepts it.
func (c *Client) NotifyTransaction(transaction Transaction) Result {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if !cfg.APMEnabled || cfg.APMSampleRate <= 0 || (cfg.APMSampleRate < 1 && rand.Float64() >= cfg.APMSampleRate) {
		return Result{Status: http.StatusNoContent}
	}
	if transaction.Environment == "" {
		transaction.Environment = cfg.Environment
	}
	if transaction.Kind == "" {
		transaction.Kind = "web"
	}
	if transaction.OccurredAt.IsZero() {
		transaction.OccurredAt = time.Now().UTC()
	}
	if transaction.Spans == nil {
		transaction.Spans = []Span{}
	}
	return c.submit(delivery{resource: "transactions", payload: transaction})
}

// NotifyLog sends one structured log event when log forwarding is enabled.
func (c *Client) NotifyLog(message, level, source string) Result {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if !cfg.LogsEnabled {
		return Result{Status: http.StatusNoContent}
	}
	payload := LogEntry{
		Message: message, Level: normalizeLogLevel(level), Source: source,
		Environment: cfg.Environment, OccurredAt: time.Now().UTC(),
	}
	return c.submit(delivery{resource: "logs", payload: payload})
}

func (c *Client) submit(item delivery) Result {
	c.mu.RLock()
	cfg := c.cfg
	c.mu.RUnlock()
	if !cfg.Async {
		return c.deliver(context.Background(), item)
	}
	c.inFlight.Add(1)
	select {
	case c.queue <- item:
		return Result{Status: http.StatusAccepted, Queued: true}
	default:
		c.inFlight.Add(-1)
		cfg.Logger.Warn("errorgap: item dropped, queue full", "queue_size", cfg.QueueSize)
		return Result{Err: fmt.Errorf("errorgap: queue full")}
	}
}

// Flush blocks until in-flight async deliveries complete.
func (c *Client) Flush(ctx context.Context) error {
	c.mu.RLock()
	async := c.cfg.Async
	c.mu.RUnlock()
	if !async {
		return nil
	}
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if len(c.queue) == 0 && c.inFlight.Load() == 0 {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close drains the queue and stops the background worker. Idempotent.
func (c *Client) Close(ctx context.Context) error {
	var err error
	c.stopOnce.Do(func() {
		close(c.queue)
		done := make(chan struct{})
		go func() { c.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-ctx.Done():
			err = ctx.Err()
		}
	})
	return err
}

func (c *Client) loop() {
	defer c.wg.Done()
	for item := range c.queue {
		c.deliver(context.Background(), item)
		c.inFlight.Add(-1)
	}
}

func (c *Client) deliver(ctx context.Context, item delivery) Result {
	body, err := json.Marshal(item.payload)
	if err != nil {
		c.cfg.Logger.Warn("errorgap: failed to encode notice", "err", err)
		return Result{Err: err}
	}

	url := fmt.Sprintf("%s/api/projects/%s/%s", trimTrailingSlash(c.cfg.Endpoint), c.cfg.ProjectSlug, item.resource)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		c.cfg.Logger.Warn("errorgap: failed to build request", "err", err)
		return Result{Err: err}
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("user-agent", "errorgap-go/"+Version)
	if c.cfg.APIKey != "" {
		req.Header.Set("x-errorgap-project-key", c.cfg.APIKey)
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		c.cfg.Logger.Warn("errorgap: delivery failed", "err", err)
		return Result{Err: err}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return Result{Status: resp.StatusCode, Body: respBody}
}

func trimTrailingSlash(s string) string {
	if len(s) > 0 && s[len(s)-1] == '/' {
		return s[:len(s)-1]
	}
	return s
}

func getRootDirectory() (string, error) {
	return os.Getwd()
}
