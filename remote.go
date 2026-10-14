package errorgap

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// remoteSettings holds the project's remote settings: what errorgap wants
// this app to send. Someone can turn errors, performance data or logs off for
// a project in errorgap (Project settings › What SDKs send) and running apps
// follow within a poll interval, with no deploy.
//
// Read from GET /api/projects/:slug/config in the background. Until the
// first answer, and whenever errorgap cannot be reached, everything stays on.
type remoteSettings struct {
	once      sync.Once
	stop      chan struct{}
	errorsOff atomic.Bool
	apmOff    atomic.Bool
	logsOff   atomic.Bool
	interval  atomic.Int64 // seconds
}

const (
	defaultRemoteInterval = 600
	minRemoteInterval     = 60
)

type remoteKind int

const (
	remoteErrors remoteKind = iota
	remoteAPM
	remoteLogs
)

// remoteEnabled reports false only when errorgap has said to stop sending
// this kind of data. The first call starts the background poll.
func (c *Client) remoteEnabled(kind remoteKind) bool {
	if c.cfg.DisableRemoteSettings {
		return true
	}
	c.remote.once.Do(func() {
		c.remote.stop = make(chan struct{})
		go c.pollRemoteSettings()
	})
	switch kind {
	case remoteAPM:
		return !c.remote.apmOff.Load()
	case remoteLogs:
		return !c.remote.logsOff.Load()
	default:
		return !c.remote.errorsOff.Load()
	}
}

func (c *Client) pollRemoteSettings() {
	for {
		c.refreshRemoteSettings(context.Background())
		seconds := c.remote.interval.Load()
		if seconds < minRemoteInterval {
			seconds = defaultRemoteInterval
		}
		select {
		case <-c.remote.stop:
			return
		case <-time.After(time.Duration(seconds) * time.Second):
		}
	}
}

// refreshRemoteSettings fetches the settings now and reports whether they
// could be read. A failure leaves the last known settings in place.
func (c *Client) refreshRemoteSettings(ctx context.Context) bool {
	url := fmt.Sprintf("%s/api/projects/%s/config", trimTrailingSlash(c.cfg.Endpoint), c.cfg.ProjectSlug)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("user-agent", "errorgap-go/"+Version)
	if c.cfg.APIKey != "" {
		req.Header.Set("x-errorgap-project-key", c.cfg.APIKey)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var body struct {
		Errors   *bool `json:"errors_enabled"`
		APM      *bool `json:"apm_enabled"`
		Logs     *bool `json:"logs_enabled"`
		Interval int64 `json:"poll_interval_seconds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	// An absent field means on.
	c.remote.errorsOff.Store(body.Errors != nil && !*body.Errors)
	c.remote.apmOff.Store(body.APM != nil && !*body.APM)
	c.remote.logsOff.Store(body.Logs != nil && !*body.Logs)
	c.remote.interval.Store(body.Interval)
	return true
}
