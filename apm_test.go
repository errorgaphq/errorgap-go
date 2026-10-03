package errorgap

import (
	"context"
	"errors"
	"log/slog"
	"strings"
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

func TestTrackJobLinksItsErrorAndContext(t *testing.T) {
	t.Setenv("ERRORGAP_ASYNC", "false")
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	client, err := NewClient(Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", APIKey: "egp_test",
		Async: false, APMEnabled: true, APMSampleRate: 1,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close(context.Background())

	var seen string
	_ = client.TrackJob(context.Background(), "ReceiptJob", "mailers", func(ctx context.Context) error {
		seen = TransactionIDFromContext(ctx)
		return errors.New("smtp down")
	})
	_ = client.Flush(context.Background())
	if len(ing.Requests()) != 2 {
		t.Fatalf("want a notice and a transaction, got %d requests", len(ing.Requests()))
	}
	if len(seen) != 36 {
		t.Fatalf("job context transaction id = %q", seen)
	}
	for _, req := range ing.Requests() {
		if strings.HasSuffix(req.Path, "/transactions") {
			if req.Body["id"] != seen {
				t.Errorf("transaction id = %v, want %s", req.Body["id"], seen)
			}
			continue
		}
		ctx, _ := req.Body["context"].(map[string]any)
		if ctx["transaction_id"] != seen {
			t.Errorf("notice transaction_id = %v, want %s", ctx["transaction_id"], seen)
		}
	}
}

func TestNotifyContextWithoutATransactionAddsNothing(t *testing.T) {
	t.Setenv("ERRORGAP_ASYNC", "false")
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	client, err := NewClient(Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", APIKey: "egp_test", Async: false,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close(context.Background())
	client.NotifyContext(context.Background(), errors.New("boot"))
	_ = client.Flush(context.Background())
	ctx, _ := ing.Requests()[0].Body["context"].(map[string]any)
	if _, set := ctx["transaction_id"]; set {
		t.Errorf("transaction_id set without a transaction: %v", ctx["transaction_id"])
	}
	if id := NewTransactionID(); len(id) != 36 || id[14] != '4' {
		t.Errorf("NewTransactionID = %q, want a v4 uuid", id)
	}
}
