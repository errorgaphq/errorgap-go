// Package testutil hosts test helpers shared across the package.
package testutil

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// CapturedRequest records one POST received by the fake ingestor.
type CapturedRequest struct {
	Path    string
	Method  string
	Headers http.Header
	Body    map[string]any
	Raw     []byte
}

// Ingestor is an in-process HTTP server that records each incoming POST.
type Ingestor struct {
	mu       sync.Mutex
	requests []CapturedRequest
	Server   *httptest.Server
	Status   int
}

// NewIngestor returns a started in-process server that responds with the
// given HTTP status (default 201) and a stub JSON body.
func NewIngestor(status int) *Ingestor {
	if status == 0 {
		status = http.StatusCreated
	}
	ing := &Ingestor{Status: status}
	ing.Server = httptest.NewServer(http.HandlerFunc(ing.handle))
	return ing
}

// Endpoint returns the base URL the SDK should be pointed at.
func (i *Ingestor) Endpoint() string { return i.Server.URL }

// Requests returns a snapshot copy of recorded requests.
func (i *Ingestor) Requests() []CapturedRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]CapturedRequest, len(i.requests))
	copy(out, i.requests)
	return out
}

// Close shuts down the underlying httptest.Server.
func (i *Ingestor) Close() { i.Server.Close() }

func (i *Ingestor) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	headers := r.Header.Clone()
	i.mu.Lock()
	i.requests = append(i.requests, CapturedRequest{
		Path:    r.URL.Path,
		Method:  r.Method,
		Headers: headers,
		Body:    body,
		Raw:     raw,
	})
	i.mu.Unlock()

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(i.Status)
	_, _ = w.Write([]byte(`{"group_id":"g_1"}`))
}
