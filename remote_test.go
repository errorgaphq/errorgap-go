package errorgap

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestRemoteSettingsSwitchAKindOffAndFailOpen(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("x-errorgap-project-key"))
		mu.Unlock()
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"errors_enabled":false,"apm_enabled":true,"poll_interval_seconds":600}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := testClient(t, Config{Endpoint: server.URL, APIKey: "key-demo", APMEnabled: true})
	if !client.refreshRemoteSettings(context.Background()) {
		t.Fatal("settings were not read")
	}
	if seen[0] != "GET /api/projects/demo/config key-demo" {
		t.Fatalf("request = %q", seen[0])
	}
	if client.remoteEnabled(remoteErrors) || !client.remoteEnabled(remoteAPM) || !client.remoteEnabled(remoteLogs) {
		t.Fatal("only errors should be off")
	}
	if status := client.Notify(errors.New("x")).Status; status != http.StatusAccepted {
		t.Fatalf("notify status = %d", status)
	}
	if status := client.NotifyTransaction(Transaction{}).Status; status != http.StatusCreated {
		t.Fatalf("transaction status = %d", status)
	}

	unreachable := testClient(t, Config{Endpoint: "http://127.0.0.1:1"})
	if unreachable.refreshRemoteSettings(context.Background()) || !unreachable.remoteEnabled(remoteErrors) {
		t.Fatal("an unreachable errorgap must leave everything on")
	}
	off := testClient(t, Config{Endpoint: server.URL, DisableRemoteSettings: true})
	if !off.remoteEnabled(remoteErrors) {
		t.Fatal("DisableRemoteSettings must ignore the switches")
	}
}
