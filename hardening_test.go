package errorgap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type stub struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

// newStub answers every request with the given status and headers.
func newStub(t *testing.T, status int, headers map[string]string) *stub {
	t.Helper()
	s := &stub{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet { // the settings poll, not an ingest request
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.mu.Unlock()
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *stub) received() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

func testClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	cfg.ProjectSlug = "demo"
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	return client
}

func TestRateLimitPausesDelivery(t *testing.T) {
	server := newStub(t, http.StatusTooManyRequests, map[string]string{"Retry-After": "120"})
	client := testClient(t, Config{Endpoint: server.URL})

	if got := client.Notify(errors.New("one")).Status; got != http.StatusTooManyRequests {
		t.Fatalf("status = %d", got)
	}
	if !client.Paused() {
		t.Fatal("expected delivery to be paused")
	}
	if err := client.Notify(errors.New("two")).Err; !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if n := len(server.received()); n != 1 {
		t.Fatalf("requests = %d", n)
	}
}

func TestOnlyNetworkAndServerErrorsAreRetried(t *testing.T) {
	cases := map[bool][]Result{
		true:  {{Status: 500}, {Err: errors.New("refused")}},
		false: {{Status: 422}, {Status: 429}, {Err: ErrRateLimited}, {Status: 201}},
	}
	for want, results := range cases {
		for _, result := range results {
			if got := retryable(result); got != want {
				t.Errorf("retryable(%+v) = %v", result, got)
			}
		}
	}
}

func TestBeforeNotifyDropsChangesAndSurvivesAPanic(t *testing.T) {
	server := newStub(t, http.StatusCreated, nil)

	testClient(t, Config{Endpoint: server.URL, BeforeNotify: func(*Notice) bool { return false }}).
		Notify(errors.New("dropped"))
	testClient(t, Config{Endpoint: server.URL, BeforeNotify: func(n *Notice) bool {
		n.Context["team"] = "billing"
		return true
	}}).Notify(errors.New("changed"))
	testClient(t, Config{Endpoint: server.URL, BeforeNotify: func(*Notice) bool { panic("bug in the hook") }}).
		Notify(errors.New("still sent"))

	bodies := server.received()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d", len(bodies))
	}
	if bodies[0]["context"].(map[string]any)["team"] != "billing" {
		t.Errorf("context = %v", bodies[0]["context"])
	}
}

func TestIgnoredEnvironmentSendsNothing(t *testing.T) {
	server := newStub(t, http.StatusCreated, nil)
	client := testClient(t, Config{Endpoint: server.URL, Environment: "test", IgnoreEnvironments: []string{"test"}})
	client.Notify(errors.New("x"))
	if n := len(server.received()); n != 0 {
		t.Fatalf("requests = %d", n)
	}
}

func TestNoticeCarriesCausesAndMasksNestedKeys(t *testing.T) {
	cfg := Config{ProjectSlug: "demo"}
	cfg.applyDefaults()
	inner := errors.New("connection reset")
	outer := fmt.Errorf("load order: %w", inner)

	notice := buildNotice(outer, &cfg, NoticeOptions{
		Params:  map[string]any{"users": []any{map[string]any{"password": "p", "name": "Ada"}}},
		Session: map[string]any{"auth_token": "t"},
	})

	causes := notice.Context["causes"].([]map[string]string)
	if len(causes) != 1 || causes[0]["message"] != "connection reset" {
		t.Errorf("causes = %v", causes)
	}
	user := notice.Params["users"].([]any)[0].(map[string]any)
	if user["password"] != filteredValue || user["name"] != "Ada" {
		t.Errorf("params = %v", notice.Params)
	}
	if notice.Session["auth_token"] != filteredValue {
		t.Errorf("session = %v", notice.Session)
	}
}

func TestUnencodableContextDoesNotLoseTheNotice(t *testing.T) {
	server := newStub(t, http.StatusCreated, nil)
	client := testClient(t, Config{Endpoint: server.URL})
	result := client.Notify(errors.New("x"), NoticeOptions{Context: map[string]any{"ch": make(chan int), "ok": 1}})
	if result.Status != http.StatusCreated {
		t.Fatalf("result = %+v", result)
	}
}

func TestQueuedDeliveriesFlush(t *testing.T) {
	server := newStub(t, http.StatusCreated, nil)
	client := testClient(t, Config{Endpoint: server.URL, Async: true})
	for i := 0; i < 5; i++ {
		client.Notify(fmt.Errorf("e%d", i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(server.received()); n != 5 {
		t.Fatalf("requests = %d", n)
	}
}
