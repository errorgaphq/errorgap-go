package stdhttp_test

import (
	"context"
	"errors"
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

// Errors reported inside a request carry its transaction id, so errorgap shows
// the error the request raised and links the occurrence to its trace.
func TestRecoverLinksTheRequestAndItsErrors(t *testing.T) {
	t.Setenv("ERRORGAP_ASYNC", "false")
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	if err := errorgap.Init(errorgap.Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", APIKey: "egp_test",
		Async: false, APMEnabled: true, APMSampleRate: 1,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer errorgap.Close(context.Background())

	app := stdhttp.Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errorgap.NotifyContext(r.Context(), errors.New("card declined"))
		panic("kaboom")
	}))
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/orders/7", nil))
	_ = errorgap.Flush(context.Background())

	var handled, panicked, transaction map[string]any
	for _, req := range ing.Requests() {
		body := req.Body
		switch {
		case strings.HasSuffix(req.Path, "/transactions"):
			transaction = body
		case errorMessage(body) == "card declined":
			handled = body
		default:
			panicked = body
		}
	}
	if transaction == nil || handled == nil || panicked == nil {
		t.Fatalf("missing deliveries: %+v", ing.Requests())
	}
	id, _ := transaction["id"].(string)
	if len(id) != 36 {
		t.Fatalf("transaction id = %q", id)
	}
	for name, notice := range map[string]map[string]any{"handled": handled, "panic": panicked} {
		ctx, _ := notice["context"].(map[string]any)
		if ctx["transaction_id"] != id {
			t.Errorf("%s notice transaction_id = %v, want %s", name, ctx["transaction_id"], id)
		}
	}
}

func TestRecoverRecordsTheBrowserTraceHeader(t *testing.T) {
	t.Setenv("ERRORGAP_ASYNC", "false")
	ing := testutil.NewIngestor(201)
	defer ing.Close()
	if err := errorgap.Init(errorgap.Config{
		Endpoint: ing.Endpoint(), ProjectSlug: "demo", APIKey: "egp_test",
		Async: false, APMEnabled: true, APMSampleRate: 1,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer errorgap.Close(context.Background())

	app := stdhttp.Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for _, header := range []string{"0192F3C4-7A1B-4C2D-9E3F-0123456789AB", "not-a-uuid"} {
		req := httptest.NewRequest(http.MethodGet, "/orders/7", nil)
		req.Header.Set("x-errorgap-trace", header)
		app.ServeHTTP(httptest.NewRecorder(), req)
	}
	_ = errorgap.Flush(context.Background())

	var traces []any
	for _, req := range ing.Requests() {
		if strings.HasSuffix(req.Path, "/transactions") {
			traces = append(traces, req.Body["trace_id"])
		}
	}
	if len(traces) != 2 || traces[0] != "0192f3c4-7a1b-4c2d-9e3f-0123456789ab" || traces[1] != nil {
		t.Fatalf("trace ids = %v", traces)
	}
}

func errorMessage(notice map[string]any) string {
	errs, _ := notice["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	first, _ := errs[0].(map[string]any)
	message, _ := first["message"].(string)
	return message
}
