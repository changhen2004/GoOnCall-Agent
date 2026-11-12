package rabbitmq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInspect_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/queues/%2F/myqueue" {
			t.Errorf("path = %s, want /api/queues/%%2F/myqueue", r.URL.EscapedPath())
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "guest" || pass != "guest" {
			t.Errorf("basic auth = %s/%s, want guest/guest", user, pass)
		}
		w.Write([]byte(`{"name":"myqueue","messages":2418,"consumers":2,"message_stats":{"publish":10,"deliver_get":3}}`))
	}))
	defer server.Close()

	tool := New(server.URL, "guest", "guest")

	out, err := tool.Run(context.Background(), `{"queue":"myqueue"}`)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out, "2418") || !strings.Contains(out, "consumers") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestInspect_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tool := New(server.URL, "guest", "guest")

	if _, err := tool.Run(context.Background(), `{"queue":"missing"}`); err == nil {
		t.Fatal("expected error for 404")
	}
}
