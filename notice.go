package errorgap

import (
	"reflect"
	"time"
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
}

func buildNotice(err error, cfg *Config, opts NoticeOptions) Notice {
	rootDir, _ := getRootDirectory()
	frames := captureBacktrace(opts.BacktraceSkip+2, rootDir)

	context := map[string]any{
		"notifier":         "errorgap-go",
		"notifier_version": Version,
		"environment":      cfg.Environment,
	}
	if cfg.Release != "" {
		context["release"] = cfg.Release
	}
	for k, v := range opts.Context {
		context[k] = v
	}

	return Notice{
		ProjectID:  cfg.ProjectID,
		ReceivedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Errors: []ErrorEntry{{
			Type:      errorType(err),
			Message:   err.Error(),
			Backtrace: frames,
		}},
		Context:     context,
		Environment: nonNilMap(opts.Environment),
		Session:     nonNilMap(opts.Session),
		Params:      FilterParams(opts.Params, cfg.FilterKeys),
	}
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

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
