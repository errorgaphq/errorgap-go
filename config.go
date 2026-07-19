package errorgap

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
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

	// RootDirectory is used to classify application frames and make their
	// filenames relative. Defaults to $ERRORGAP_ROOT_DIRECTORY or the current
	// working directory.
	RootDirectory string

	// Async controls fire-and-forget delivery. Defaults to true.
	Async bool

	// Logger receives SDK warnings. Defaults to a discard logger.
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
	// Drops the new item when full. Defaults to 100.
	QueueSize int

	// CaptureGlobals installs a recover-and-log hook at the entry point.
	// (Go doesn't allow process-wide panic handlers; use the middleware
	// adapters instead.) Currently unused; reserved for future use.
	CaptureGlobals bool

	// APMEnabled controls transaction delivery. Defaults to
	// $ERRORGAP_APM_ENABLED or false.
	APMEnabled bool

	// APMSampleRate is the fraction of transactions to send, from 0 to 1.
	// Defaults to $ERRORGAP_APM_SAMPLE_RATE or 1.
	APMSampleRate float64

	// LogsEnabled controls structured log delivery. Defaults to
	// $ERRORGAP_LOGS_ENABLED or false.
	LogsEnabled bool

	// MinimumLogLevel is used by NewSlogHandler. Defaults to
	// $ERRORGAP_MINIMUM_LOG_LEVEL or slog.LevelWarn.
	MinimumLogLevel slog.Level
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
	if c.RootDirectory == "" {
		c.RootDirectory = os.Getenv("ERRORGAP_ROOT_DIRECTORY")
		if c.RootDirectory == "" {
			c.RootDirectory, _ = getRootDirectory()
		}
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
	if value, ok := os.LookupEnv("ERRORGAP_APM_ENABLED"); ok {
		c.APMEnabled = parseBool(value, c.APMEnabled)
	}
	if value, ok := os.LookupEnv("ERRORGAP_LOGS_ENABLED"); ok {
		c.LogsEnabled = parseBool(value, c.LogsEnabled)
	}
	if c.APMSampleRate == 0 {
		c.APMSampleRate = 1
		if value := os.Getenv("ERRORGAP_APM_SAMPLE_RATE"); value != "" {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				c.APMSampleRate = parsed
			}
		}
	}
	if c.APMSampleRate < 0 {
		c.APMSampleRate = 0
	}
	if c.APMSampleRate > 1 {
		c.APMSampleRate = 1
	}
	if value := os.Getenv("ERRORGAP_MINIMUM_LOG_LEVEL"); value != "" {
		c.MinimumLogLevel = parseLogLevel(value)
	} else if c.MinimumLogLevel == 0 {
		c.MinimumLogLevel = slog.LevelWarn
	}
	// Async defaults to true unless explicitly overridden. Since the zero
	// value of bool is false, callers MUST set Async=true explicitly OR
	// use the Init helper which preserves the intended default.
}

func parseBool(value string, fallback bool) bool {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseLogLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err == nil {
		return level
	}
	return slog.LevelWarn
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
