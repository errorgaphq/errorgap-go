package stdhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	errorgap "github.com/errorgaphq/errorgap-go"
	"github.com/errorgaphq/errorgap-go/internal/testutil"
	"github.com/errorgaphq/errorgap-go/stdhttp"
)

func TestRecoverNotifiesOnPanic(t *testing.T) {
	ing := testutil.NewIngestor(201)
	defer ing.Close()

	if err := errorgap.Init(errorgap.Config{
		Endpoint:    ing.Endpoint(),
		ProjectSlug: "demo",
		APIKey:      "flk_test",
		Async:       false,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer errorgap.Close(context.Background())

	app := stdhttp.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom?x=1", nil)
	app.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}

	// Allow async deliver goroutine to land if Async slipped through.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(ing.Requests()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	reqs := ing.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	body := reqs[0].Body
	errs := body["errors"].([]any)
	first := errs[0].(map[string]any)
	if first["message"] != "kaboom" {
		t.Errorf("message = %v, want kaboom", first["message"])
	}
	ctx := body["context"].(map[string]any)
	if ctx["source"] != "stdhttp" {
		t.Errorf("source = %v", ctx["source"])
	}
}
