package errorgap

import (
	"errors"
	"reflect"
	"time"
)

const (
	maxCauseDepth    = 10
	maxMessageLength = 8192
)

// ErrorEntry is one entry in the notice's errors array.
type ErrorEntry struct {
	Type      string  `json:"type"`
	Message   string  `json:"message"`
	Backtrace []Frame `json:"backtrace"`
}

// Notice is the wire envelope POSTed to /api/projects/:slug/notices.
type Notice struct {
	ProjectID   string         `json:"project_id,omitempty"`
	ReceivedAt  string         `json:"received_at"`
	Errors      []ErrorEntry   `json:"errors"`
	Context     map[string]any `json:"context"`
	Environment map[string]any `json:"environment"`
	Session     map[string]any `json:"session"`
	Params      map[string]any `json:"params"`
}

// NoticeOptions allows callers to add per-notice context.
type NoticeOptions struct {
	Context     map[string]any
	Environment map[string]any
	Session     map[string]any
	Params      map[string]any

	// Skip extra runtime.Caller frames when capturing the backtrace.
	// Useful when Notify is wrapped by another helper.
	BacktraceSkip int

	// TransactionID is the APM transaction the error was raised in, sent as
	// context.transaction_id. NotifyContext fills it from the context.
	TransactionID string
}

func buildNotice(err error, cfg *Config, opts NoticeOptions) Notice {
	frames := captureBacktrace(opts.BacktraceSkip+2, cfg.RootDirectory)

	context := map[string]any{
		"notifier":         "errorgap-go",
		"notifier_version": Version,
		"environment":      cfg.Environment,
	}
	if cfg.Release != "" {
		context["release"] = cfg.Release
	}
	if cfg.RootDirectory != "" {
		context["root_directory"] = cfg.RootDirectory
	}
	if causes := errorCauses(err); len(causes) > 0 {
		context["causes"] = causes
	}
	for k, v := range cleanMap(opts.Context) {
		context[k] = v
	}
	if _, set := context["transaction_id"]; !set && opts.TransactionID != "" {
		context["transaction_id"] = opts.TransactionID
	}

	return Notice{
		ProjectID:  cfg.ProjectID,
		ReceivedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Errors: []ErrorEntry{{
			Type:      errorType(err),
			Message:   truncateString(err.Error(), maxMessageLength),
			Backtrace: frames,
		}},
		Context:     context,
		Environment: cleanMap(opts.Environment),
		Session:     FilterParams(opts.Session, cfg.FilterKeys),
		Params:      FilterParams(opts.Params, cfg.FilterKeys),
	}
}

// errorCauses walks errors.Unwrap (the %w chain) and returns the type and
// message of each wrapped error, nearest first.
func errorCauses(err error) []map[string]string {
	var causes []map[string]string
	for current := errors.Unwrap(err); current != nil && len(causes) < maxCauseDepth; current = errors.Unwrap(current) {
		causes = append(causes, map[string]string{
			"type":    errorType(current),
			"message": truncateString(current.Error(), maxMessageLength),
		})
	}
	return causes
}

func errorType(err error) string {
	t := reflect.TypeOf(err)
	if t == nil {
		return "error"
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Name() == "" {
		return "error"
	}
	return t.Name()
}
