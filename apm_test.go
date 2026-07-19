package errorgap

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/errorgaphq/errorgap-go/internal/testutil"
)

func TestNotifyTransactionAndLogUseCanonicalEndpoints(t *testing.T) {
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	c, err := NewClient(Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", APIKey: "flk_test",
		Async: false, APMEnabled: true, LogsEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, collector := WithSpanCollector(context.Background())
	RecordDatabase(ctx, "SELECT 42 FROM orders WHERE id = 7 AND name = 'alice'", 12*time.Millisecond)
	RecordExternal(ctx, 8*time.Millisecond)
	transaction := Transaction{
		Kind: "web", Method: "GET", Path: "/orders/{id}", PathRaw: "/orders/7",
		StatusCode: 200, DurationMS: 25, OccurredAt: time.Now().UTC(), Spans: collector.Spans(),
	}
	if result := c.NotifyTransaction(transaction); !result.Success() {
		t.Fatalf("transaction result = %+v", result)
	}
	if result := c.NotifyLog("gateway timeout", "WARNING", "checkout"); !result.Success() {
		t.Fatalf("log result = %+v", result)
	}

	requests := ing.Requests()
	if got := requests[0].Path; got != "/api/projects/demo/transactions" {
		t.Errorf("transaction path = %q", got)
	}
	if got := requests[1].Path; got != "/api/projects/demo/logs" {
		t.Errorf("log path = %q", got)
	}
	spans := requests[0].Body["spans"].([]any)
	query := spans[0].(map[string]any)["sql"]
	if query != "SELECT ? FROM orders WHERE id = ? AND name = ?" {
		t.Errorf("normalized SQL = %v", query)
	}
	if kind := spans[1].(map[string]any)["kind"]; kind != "http" {
		t.Errorf("external span kind = %v, want http", kind)
	}
	if requests[1].Body["level"] != "warn" {
		t.Errorf("log level = %v", requests[1].Body["level"])
	}
}

func TestTrackJobSendsErrorAndJobTransaction(t *testing.T) {
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	c, err := NewClient(Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", Async: false,
		APMEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("receipt failed")
	err = c.TrackJob(context.Background(), "ReceiptJob", "critical", func(ctx context.Context) error {
		RecordDatabase(ctx, "SELECT 9", time.Millisecond)
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("TrackJob error = %v", err)
	}
	requests := ing.Requests()
	if len(requests) != 2 || requests[0].Path != "/api/projects/demo/notices" || requests[1].Path != "/api/projects/demo/transactions" {
		t.Fatalf("requests = %+v", requests)
	}
	if requests[1].Body["kind"] != "job" || requests[1].Body["job_class"] != "ReceiptJob" {
		t.Errorf("job payload = %+v", requests[1].Body)
	}
}

func TestSlogHandlerForwardsOnlyMinimumLevel(t *testing.T) {
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	c, err := NewClient(Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", Async: false, LogsEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(NewSlogHandler(nil, c, slog.LevelWarn))
	logger.Info("ignored")
	logger.Warn("forwarded")
	requests := ing.Requests()
	if len(requests) != 1 || requests[0].Body["message"] != "forwarded" {
		t.Fatalf("log requests = %+v", requests)
	}
}
