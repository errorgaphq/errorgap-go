package errorgap

import (
	"context"
	"log/slog"
	"runtime"
)

// SlogHandler forwards records at or above minimumLevel to Errorgap while
// preserving delivery to the wrapped handler. Pass nil for client to use the
// package-level client configured by Init.
type SlogHandler struct {
	next    slog.Handler
	client  *Client
	minimum slog.Level
	attrs   []slog.Attr
	groups  []string
}

// NewSlogHandler returns a standard library slog handler with Errorgap log
// forwarding. A nil wrapped handler discards local output.
func NewSlogHandler(next slog.Handler, client *Client, minimumLevel slog.Level) *SlogHandler {
	if next == nil {
		next = slog.NewTextHandler(discardWriter{}, nil)
	}
	return &SlogHandler{next: next, client: client, minimum: minimumLevel}
}

func (h *SlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level) || level >= h.minimum
}

func (h *SlogHandler) Handle(ctx context.Context, record slog.Record) error {
	err := h.next.Handle(ctx, record)
	if record.Level < h.minimum {
		return err
	}
	source := "slog"
	if record.PC != 0 {
		if frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next(); frame.Function != "" {
			// Function is stable across repeated records. Including the source
			// line would split otherwise identical messages into separate
			// aggregate groups when adjacent log calls are used.
			source = frame.Function
		}
	}
	if h.client != nil {
		h.client.NotifyLog(record.Message, record.Level.String(), source)
	} else {
		NotifyLog(record.Message, record.Level.String(), source)
	}
	return err
}

func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.next = h.next.WithAttrs(attrs)
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	return &clone
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	clone := *h
	clone.next = h.next.WithGroup(name)
	clone.groups = append(append([]string(nil), h.groups...), name)
	return &clone
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
