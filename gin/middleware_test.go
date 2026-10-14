package errorgapgin_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	errorgap "github.com/errorgaphq/errorgap-go"
	errorgapgin "github.com/errorgaphq/errorgap-go/gin"
	"github.com/gin-gonic/gin"
)

type received struct {
	path string
	body map[string]any
}

func ingestor(t *testing.T) func() []received {
	t.Helper()
	var mu sync.Mutex
	var got []received
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		got = append(got, received{r.URL.Path, body})
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)
	if err := errorgap.Init(errorgap.Config{Endpoint: server.URL, ProjectSlug: "demo", APMEnabled: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = errorgap.Close(context.Background()) })
	return func() []received {
		// Delivery is queued: wait for it.
		_ = errorgap.Flush(context.Background())
		mu.Lock()
		defer mu.Unlock()
		return append([]received(nil), got...)
	}
}

func of(all []received, suffix string) []map[string]any {
	var out []map[string]any
	for _, r := range all {
		if strings.HasSuffix(r.path, suffix) {
			out = append(out, r.body)
		}
	}
	return out
}

func TestGinMiddleware(t *testing.T) {
	requests := ingestor(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery(), errorgapgin.Middleware())
	router.GET("/orders/:id", func(c *gin.Context) { c.String(http.StatusOK, c.Param("id")) })
	router.GET("/panic", func(c *gin.Context) { panic("kaboom") })
	router.GET("/failed", func(c *gin.Context) {
		_ = c.Error(errors.New("payment gateway down"))
		c.Status(http.StatusBadGateway)
	})

	for path, want := range map[string]int{"/orders/7": 200, "/panic": 500, "/failed": 502} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != want {
			t.Errorf("%s: status = %d, want %d", path, recorder.Code, want)
		}
	}

	messages := map[string]bool{}
	for _, notice := range of(requests(), "/notices") {
		first := notice["errors"].([]any)[0].(map[string]any)
		messages[first["message"].(string)] = true
		if notice["context"].(map[string]any)["transaction_id"] == nil {
			t.Errorf("notice is not linked to its request: %v", notice["context"])
		}
	}
	if !messages["kaboom"] || !messages["payment gateway down"] || len(messages) != 2 {
		t.Errorf("reported = %v", messages)
	}

	routes := map[string]float64{}
	for _, txn := range of(requests(), "/transactions") {
		routes[txn["path"].(string)] = txn["status_code"].(float64)
	}
	if routes["/orders/:id"] != 200 || routes["/panic"] != 500 || routes["/failed"] != 502 {
		t.Errorf("transactions = %v", routes)
	}
}
