package errorgap

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gitlab.jgrubbs.net/jGRUBBS/errorgap-go/internal/testutil"
)

func TestClientSyncPostsToNoticesWithCanonicalHeaders(t *testing.T) {
	ing := testutil.NewIngestor(201)
	defer ing.Close()

	c, err := NewClient(Config{
		Endpoint:    ing.Endpoint(),
		ProjectSlug: "demo",
		APIKey:      "flk_test",
		Async:       false,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	res := c.Notify(errors.New("boom"))
	if !res.Success() {
		t.Fatalf("res = %+v, want success", res)
	}

	reqs := ing.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != "POST" {
		t.Errorf("method = %s, want POST", req.Method)
	}
	if req.Path != "/api/projects/demo/notices" {
		t.Errorf("path = %s", req.Path)
	}
	if got := req.Headers.Get("x-errorgap-project-key"); got != "flk_test" {
		t.Errorf("auth header = %q", got)
	}
	if ua := req.Headers.Get("user-agent"); !strings.HasPrefix(ua, "errorgap-go/") {
		t.Errorf("user-agent = %q", ua)
	}
}

func TestClientSendsFullNoticeEnvelope(t *testing.T) {
	ing := testutil.NewIngestor(201)
	defer ing.Close()

	c, err := NewClient(Config{
		Endpoint:    ing.Endpoint(),
		ProjectSlug: "demo",
		APIKey:      "flk_test",
		Async:       false,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.Notify(errors.New("kaboom"))

	body := ing.Requests()[0].Body
	if body["errors"] == nil {
		t.Fatal("errors missing from body")
	}
	if body["context"] == nil {
		t.Fatal("context missing from body")
	}
}

func TestClientAsyncQueuesAndFlushes(t *testing.T) {
	ing := testutil.NewIngestor(201)
	defer ing.Close()

	c, err := NewClient(Config{
		Endpoint:    ing.Endpoint(),
		ProjectSlug: "demo",
		APIKey:      "flk_test",
		Async:       true,
		QueueSize:   16,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close(context.Background())

	res := c.Notify(errors.New("queued"))
	if !res.Queued {
		t.Fatalf("res = %+v, want queued", res)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Give the deliver goroutine a moment to land.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(ing.Requests()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(ing.Requests()) != 1 {
		t.Fatalf("requests after flush = %d, want 1", len(ing.Requests()))
	}
}

func TestNewClientRejectsMissingSlug(t *testing.T) {
	_, err := NewClient(Config{Endpoint: "https://e.example.com"})
	if !errors.Is(err, ErrMissingProjectSlug) {
		t.Errorf("err = %v, want ErrMissingProjectSlug", err)
	}
}
