package errorgap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	closed   atomic.Bool
	backlog  atomic.Int64
	// pausedUntil is a unix-nano time: sending is paused until then after a
	// 429 or 503 (Retry-After).
	pausedUntil atomic.Int64
}

type delivery struct {
	resource string
	payload  any
	attempt  int
}

// ErrRateLimited is returned while delivery is paused at the server's
// request.
var ErrRateLimited = errors.New("errorgap: rate limited by the server, not sent")

const (
	maxAttempts       = 3
	retryDelay        = 30 * time.Second
	backlogSize       = 100
	defaultRetryAfter = 60
	maxRetryAfter     = 300
)

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

	if cfg.ignoredEnvironment() {
		return Result{Status: http.StatusAccepted}
	}
	notice := buildNotice(err, &cfg, o)
	if cfg.BeforeNotify != nil && !runHook(&cfg, &notice) {
		return Result{Status: http.StatusAccepted}
	}
	return c.submit(delivery{resource: "notices", payload: notice})
}

// NotifyContext is Notify for code running inside a transaction: the error
// carries the transaction id ctx holds (see WithTransactionID), which links it
// to its request or job in errorgap.
func (c *Client) NotifyContext(ctx context.Context, err error, opts ...NoticeOptions) Result {
	var o NoticeOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	if o.TransactionID == "" {
		o.TransactionID = TransactionIDFromContext(ctx)
	}
	o.BacktraceSkip++
	return c.Notify(err, o)
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
	if cfg.ignoredEnvironment() {
		return Result{Status: http.StatusNoContent}
	}
	if item.attempt == 0 {
		item.attempt = 1
	}
	if !cfg.Async {
		return c.deliver(context.Background(), item)
	}
	if c.closed.Load() {
		return Result{Err: fmt.Errorf("errorgap: client closed")}
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
		c.closed.Store(true)
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
		result := c.deliver(context.Background(), item)
		if retryable(result) {
			c.retryLater(item)
		}
		c.inFlight.Add(-1)
	}
}

// runHook calls BeforeNotify. A hook that panics is logged and skipped: a
// bug in a filter must not lose the error it was filtering.
func runHook(cfg *Config, notice *Notice) (keep bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			cfg.Logger.Warn("errorgap: BeforeNotify panicked", "panic", recovered)
			keep = true
		}
	}()
	return cfg.BeforeNotify(notice)
}

// retryable reports whether a failed delivery is worth another try: network
// failures and 5xx responses are. A 4xx is not (the server understood and
// refused), and after a 429 the payload is dropped so a limited project is
// not sent the same burst again.
func retryable(result Result) bool {
	if errors.Is(result.Err, ErrRateLimited) {
		return false
	}
	if result.Err != nil {
		return true
	}
	return result.Status >= 500
}

func (c *Client) retryLater(item delivery) {
	if item.attempt >= maxAttempts || c.backlog.Load() >= backlogSize || c.closed.Load() {
		return
	}
	c.backlog.Add(1)
	next := delivery{resource: item.resource, payload: item.payload, attempt: item.attempt + 1}
	time.AfterFunc(retryDelay*time.Duration(item.attempt), func() {
		c.backlog.Add(-1)
		defer func() { _ = recover() }() // the queue may have been closed meanwhile
		if c.closed.Load() {
			return
		}
		c.inFlight.Add(1)
		select {
		case c.queue <- next:
		default:
			c.inFlight.Add(-1)
		}
	})
}

// Paused reports whether delivery is paused at the server's request.
func (c *Client) Paused() bool {
	return time.Now().UnixNano() < c.pausedUntil.Load()
}

func (c *Client) pauseFor(header string) {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 1 {
		seconds = defaultRetryAfter
	}
	if seconds > maxRetryAfter {
		seconds = maxRetryAfter
	}
	c.pausedUntil.Store(time.Now().Add(time.Duration(seconds) * time.Second).UnixNano())
	c.cfg.Logger.Warn("errorgap: rate limited, pausing delivery", "seconds", seconds)
}

// encode marshals a payload. A notice holding a value encoding/json cannot
// carry (a channel, a func, a cyclic struct) is sent with that map replaced
// by its %v form rather than being lost.
func encode(payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err == nil {
		return body, nil
	}
	notice, ok := payload.(Notice)
	if !ok {
		return nil, err
	}
	for _, m := range []*map[string]any{&notice.Context, &notice.Environment, &notice.Session, &notice.Params} {
		if _, mapErr := json.Marshal(*m); mapErr != nil {
			*m = stringifyMap(*m)
		}
	}
	return json.Marshal(notice)
}

func stringifyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, err := json.Marshal(v); err != nil {
			out[k] = truncateString(fmt.Sprintf("%v", v), maxStringLen)
		} else {
			out[k] = v
		}
	}
	return out
}

func (c *Client) deliver(ctx context.Context, item delivery) Result {
	if c.Paused() {
		return Result{Err: ErrRateLimited}
	}
	body, err := encode(item.payload)
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

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		c.pauseFor(resp.Header.Get("Retry-After"))
	}
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
