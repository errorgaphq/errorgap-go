package errorgap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
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
	queue    chan Notice
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// NewClient validates the config, applies defaults, and starts the async
// delivery goroutine. The caller should defer Close() to flush in-flight
// deliveries during shutdown.
func NewClient(cfg Config) (*Client, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, queue: make(chan Notice, cfg.QueueSize)}
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

	if !cfg.Async {
		return c.deliver(context.Background(), notice)
	}

	select {
	case c.queue <- notice:
		return Result{Status: 202, Queued: true}
	default:
		// Queue full — drop the new notice rather than blocking. Logs a
		// warning so operators can size QueueSize up.
		cfg.Logger.Warn("errorgap: notice dropped, queue full",
			"queue_size", cfg.QueueSize)
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
	done := make(chan struct{})
	go func() {
		for len(c.queue) > 0 {
			// busy wait briefly; sleep handled by select below
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
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
	for notice := range c.queue {
		c.deliver(context.Background(), notice)
	}
}

func (c *Client) deliver(ctx context.Context, notice Notice) Result {
	body, err := json.Marshal(notice)
	if err != nil {
		c.cfg.Logger.Warn("errorgap: failed to encode notice", "err", err)
		return Result{Err: err}
	}

	url := fmt.Sprintf("%s/api/projects/%s/notices", trimTrailingSlash(c.cfg.Endpoint), c.cfg.ProjectSlug)
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
