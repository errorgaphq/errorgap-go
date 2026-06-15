package errorgap

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultFilterKeys are matched (case-insensitive substring) against
// param keys to mask sensitive values before delivery.
var DefaultFilterKeys = []string{
	"password",
	"password_confirmation",
	"token",
	"secret",
	"api_key",
	"authorization",
	"cookie",
}

// Config controls notifier behavior.
type Config struct {
	// Endpoint is the base URL of the Errorgap server (no trailing slash).
	// Defaults to $ERRORGAP_ENDPOINT or http://127.0.0.1:3030.
	Endpoint string

	// ProjectSlug is the slug used in the ingestion URL.
	// Defaults to $ERRORGAP_PROJECT_SLUG. Required.
	ProjectSlug string

	// ProjectID is optional and embedded in the notice payload.
	// Defaults to $ERRORGAP_PROJECT_ID.
	ProjectID string

	// APIKey is sent as the x-errorgap-project-key header.
	// Defaults to $ERRORGAP_API_KEY.
	APIKey string

	// Environment labels the deployment ("production", "staging").
	// Defaults to $ERRORGAP_ENVIRONMENT or "production".
	Environment string

	// Release is the application version embedded in the notice context.
	Release string

	// Async controls fire-and-forget delivery. Defaults to true.
	Async bool

	// Logger receives SDK warnings. Defaults to slog.Default().
	// Set to a no-op handler to silence.
	Logger *slog.Logger

	// FilterKeys overrides DefaultFilterKeys.
	FilterKeys []string

	// HTTPClient lets callers plug in a custom transport.
	// Defaults to a copy of http.DefaultClient with a 5s timeout.
	HTTPClient *http.Client

	// Timeout for the default HTTP client. Ignored if HTTPClient is set.
	Timeout time.Duration

	// QueueSize bounds the in-flight notice channel when Async is true.
	// Drops oldest in-flight notice when full. Defaults to 100.
	QueueSize int

	// CaptureGlobals installs a recover-and-log hook at the entry point.
	// (Go doesn't allow process-wide panic handlers; use the middleware
	// adapters instead.) Currently unused; reserved for future use.
	CaptureGlobals bool
}

func (c *Config) applyDefaults() {
	if c.Endpoint == "" {
		c.Endpoint = firstNonEmpty(os.Getenv("ERRORGAP_ENDPOINT"), "http://127.0.0.1:3030")
	}
	if c.ProjectSlug == "" {
		c.ProjectSlug = os.Getenv("ERRORGAP_PROJECT_SLUG")
	}
	if c.ProjectID == "" {
		c.ProjectID = os.Getenv("ERRORGAP_PROJECT_ID")
	}
	if c.APIKey == "" {
		c.APIKey = os.Getenv("ERRORGAP_API_KEY")
	}
	if c.Environment == "" {
		c.Environment = firstNonEmpty(os.Getenv("ERRORGAP_ENVIRONMENT"), "production")
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.FilterKeys == nil {
		c.FilterKeys = DefaultFilterKeys
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: c.Timeout}
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 100
	}
	// Async defaults to true unless explicitly overridden. Since the zero
	// value of bool is false, callers MUST set Async=true explicitly OR
	// use the Init helper which preserves the intended default.
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.ProjectSlug) == "" {
		return ErrMissingProjectSlug
	}
	if strings.TrimSpace(c.Endpoint) == "" {
		return ErrMissingEndpoint
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
