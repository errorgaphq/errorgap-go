package stdhttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	errorgap "github.com/errorgaphq/errorgap-go"
	"github.com/errorgaphq/errorgap-go/internal/testutil"
	"github.com/errorgaphq/errorgap-go/stdhttp"
)

func TestRecoverNotifiesOnPanic(t *testing.T) {
	t.Setenv("ERRORGAP_ASYNC", "false")
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

func TestRecoverRecordsNormalizedRouteAPMAndFilteredRequest(t *testing.T) {
	t.Setenv("ERRORGAP_ASYNC", "false")
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	if err := errorgap.Init(errorgap.Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", APIKey: "flk_test",
		APMEnabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	defer errorgap.Close(context.Background())

	mux := http.NewServeMux()
	mux.Handle("POST /orders/{orderId}", stdhttp.Route("/orders/{orderId}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		errorgap.RecordDatabase(r.Context(), "SELECT 42 WHERE id = 7", 2*time.Millisecond)
		panic("checkout exploded")
	})))
	form := url.Values{"customer": {"alice"}, "password": {"secret"}}
	req := httptest.NewRequest(http.MethodPost, "/orders/42?api_token=hidden", strings.NewReader(form.Encode()))
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	stdhttp.Recover(mux).ServeHTTP(rr, req)

	requests := ing.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want notice + transaction", len(requests))
	}
	contextBody := requests[0].Body["context"].(map[string]any)
	if contextBody["component"] != "/orders/{orderId}" {
		t.Errorf("component = %v", contextBody["component"])
	}
	params := requests[0].Body["params"].(map[string]any)
	formBody := params["form"].(map[string]any)
	if formBody["password"] != "[FILTERED]" {
		t.Errorf("password = %v", formBody["password"])
	}
	transaction := requests[1].Body
	if transaction["path"] != "/orders/{orderId}" || transaction["status_code"] != float64(500) {
		t.Errorf("transaction = %+v", transaction)
	}
	if len(transaction["spans"].([]any)) != 1 {
		t.Errorf("spans = %+v", transaction["spans"])
	}
}
