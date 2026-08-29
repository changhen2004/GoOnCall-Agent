package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuery_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("path = %s, want /api/v1/query", r.URL.Path)
		}
		if r.URL.Query().Get("query") == "" {
			t.Error("query param should not be empty")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"service":"gocommunity"},"value":[1700000000,"0.5"]}]}}`))
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, err := tool.EinoTool()
	if err != nil {
		t.Fatalf("EinoTool() error = %v", err)
	}

	out, err := einoTool.InvokableRun(context.Background(), `{"query":"rate(http_requests_total[5m])"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, "resultType") || !strings.Contains(out, "gocommunity") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestQuery_NonSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"invalid expression"}`))
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, _ := tool.EinoTool()

	if _, err := einoTool.InvokableRun(context.Background(), `{"query":"invalid"}`); err == nil {
		t.Fatal("expected error for non-success prometheus response")
	}
}

func TestQuery_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, _ := tool.EinoTool()

	if _, err := einoTool.InvokableRun(context.Background(), `{"query":"up"}`); err == nil {
		t.Fatal("expected error for 500 response")
	}
}
