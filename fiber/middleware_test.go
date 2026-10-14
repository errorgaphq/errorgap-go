package errorgapfiber_test

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
	errorgapfiber "github.com/errorgaphq/errorgap-go/fiber"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
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

func TestFiberMiddleware(t *testing.T) {
	requests := ingestor(t)
	app := fiber.New()
	app.Use(recover.New(), errorgapfiber.Middleware())
	app.Get("/orders/:id", func(c *fiber.Ctx) error { return c.SendString(c.Params("id")) })
	app.Get("/panic", func(c *fiber.Ctx) error { panic("kaboom") })
	app.Get("/failed", func(c *fiber.Ctx) error { return errors.New("payment gateway down") })
	app.Get("/missing", func(c *fiber.Ctx) error { return fiber.NewError(http.StatusNotFound, "no such order") })

	for path, want := range map[string]int{"/orders/7": 200, "/panic": 500, "/failed": 500, "/missing": 404} {
		response, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if response.StatusCode != want {
			t.Errorf("%s: status = %d, want %d", path, response.StatusCode, want)
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
	if routes["/orders/:id"] != 200 || routes["/panic"] != 500 || routes["/failed"] != 500 || routes["/missing"] != 404 {
		t.Errorf("transactions = %v", routes)
	}
}
