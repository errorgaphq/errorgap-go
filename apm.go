package errorgap

import (
	"context"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Span is one timed operation within an APM transaction.
type Span struct {
	Kind       string  `json:"kind"`
	SQL        string  `json:"sql,omitempty"`
	File       string  `json:"file,omitempty"`
	Line       int     `json:"line,omitempty"`
	Function   string  `json:"fn_name,omitempty"`
	DurationMS float64 `json:"duration_ms"`
}

// Transaction is a web request or background job sent to the APM endpoint.
type Transaction struct {
	Kind        string    `json:"kind"`
	Method      string    `json:"method,omitempty"`
	Path        string    `json:"path,omitempty"`
	PathRaw     string    `json:"path_raw,omitempty"`
	StatusCode  int       `json:"status_code,omitempty"`
	DurationMS  float64   `json:"duration_ms"`
	Environment string    `json:"environment,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
	Spans       []Span    `json:"spans"`
	JobClass    string    `json:"job_class,omitempty"`
	Queue       string    `json:"queue,omitempty"`
}

// LogEntry is the wire payload accepted by the Errorgap logs endpoint.
type LogEntry struct {
	Message     string    `json:"message"`
	Level       string    `json:"level"`
	Source      string    `json:"source,omitempty"`
	Environment string    `json:"environment,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type spanCollectorKey struct{}

// SpanCollector safely accumulates spans for a request or job.
type SpanCollector struct {
	mu    sync.Mutex
	spans []Span
}

// WithSpanCollector attaches a new span collector to ctx.
func WithSpanCollector(ctx context.Context) (context.Context, *SpanCollector) {
	collector := &SpanCollector{}
	return context.WithValue(ctx, spanCollectorKey{}, collector), collector
}

// Spans returns a snapshot of recorded spans.
func (c *SpanCollector) Spans() []Span {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Span(nil), c.spans...)
}

func (c *SpanCollector) add(span Span) {
	c.mu.Lock()
	c.spans = append(c.spans, span)
	c.mu.Unlock()
}

// RecordDatabase records a normalized database query against the active
// request or job transaction.
func RecordDatabase(ctx context.Context, sql string, duration time.Duration) {
	recordSpan(ctx, "db", NormalizeSQL(sql), duration)
}

// RecordExternal records an outbound request span against the active
// request or job transaction.
func RecordExternal(ctx context.Context, duration time.Duration) {
	recordSpan(ctx, "http", "", duration)
}

func recordSpan(ctx context.Context, kind, sql string, duration time.Duration) {
	collector, _ := ctx.Value(spanCollectorKey{}).(*SpanCollector)
	if collector == nil || duration <= 0 {
		return
	}
	file, line, function := applicationCallsite()
	collector.add(Span{
		Kind: kind, SQL: sql, File: file, Line: line, Function: function,
		DurationMS: float64(duration) / float64(time.Millisecond),
	})
}

var (
	quotedSQL = regexp.MustCompile(`'(?:''|[^'])*'`)
	numberSQL = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	spaceSQL  = regexp.MustCompile(`\s+`)
)

// NormalizeSQL replaces string and numeric literals so equivalent queries
// aggregate into one APM row.
func NormalizeSQL(sql string) string {
	sql = quotedSQL.ReplaceAllString(sql, "?")
	sql = numberSQL.ReplaceAllString(sql, "?")
	return strings.TrimSpace(spaceSQL.ReplaceAllString(sql, " "))
}

func applicationCallsite() (string, int, string) {
	pcs := make([]uintptr, 24)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "github.com/errorgaphq/errorgap-go.") &&
			!strings.Contains(frame.Function, "github.com/errorgaphq/errorgap-go/stdhttp.") &&
			!strings.HasPrefix(frame.Function, "runtime.") {
			return frame.File, frame.Line, frame.Function
		}
		if !more {
			break
		}
	}
	return "", 0, ""
}

// TrackJob runs operation as a background-job transaction. Returned errors
// are reported as notices and re-returned to the caller.
func (c *Client) TrackJob(ctx context.Context, jobClass, queue string, operation func(context.Context) error) error {
	if queue == "" {
		queue = "default"
	}
	started := time.Now()
	jobCtx, collector := WithSpanCollector(ctx)
	err := operation(jobCtx)
	status := 200
	if err != nil {
		status = 500
		c.Notify(err, NoticeOptions{
			Context:     map[string]any{"source": "errorgap-go job", "component": "go.job", "action": jobClass},
			Environment: map[string]any{"queue": queue},
		})
	}
	c.NotifyTransaction(Transaction{
		Kind: "job", JobClass: jobClass, Queue: queue, StatusCode: status,
		DurationMS: float64(time.Since(started)) / float64(time.Millisecond),
		OccurredAt: started.UTC(), Spans: collector.Spans(),
	})
	return err
}

func normalizeLogLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "warning", "warn":
		return "warn"
	case "err":
		return "error"
	case "debug", "info", "error", "fatal", "trace":
		return level
	default:
		return "info"
	}
}
