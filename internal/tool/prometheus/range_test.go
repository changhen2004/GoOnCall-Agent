package prometheus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRangeQuery_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			t.Errorf("path = %s, want /api/v1/query_range", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("query") == "" || q.Get("start") == "" || q.Get("end") == "" || q.Get("step") == "" {
			t.Errorf("missing range params: %v", q)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"handler":"/api/x"},"values":[[1700000000,"0.1"],[1700000060,"0.2"]]}
		]}}`))
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, err := tool.RangeEinoTool()
	if err != nil {
		t.Fatalf("RangeEinoTool() error = %v", err)
	}

	out, err := einoTool.InvokableRun(context.Background(), `{"query":"rate(http_requests_total[1m])"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, "0.1") || !strings.Contains(out, "0.2") || !strings.Contains(out, "/api/x") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRangeQuery_TruncatesTo100Points(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vals := make([]any, 0, 150)
		for i := 0; i < 150; i++ {
			vals = append(vals, []any{float64(1700000000 + i*60), "1"})
		}
		payload, _ := json.Marshal(vals)
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":` + string(payload) + `}]}}`))
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, _ := tool.RangeEinoTool()

	out, err := einoTool.InvokableRun(context.Background(), `{"query":"up"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, `"truncated":true`) {
		t.Fatalf("expected truncated=true for >100 points: %s", out)
	}
}

func TestRangeQuery_RequiresQuery(t *testing.T) {
	tool := New("http://example.com")
	einoTool, _ := tool.RangeEinoTool()
	if _, err := einoTool.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("expected error when query is empty")
	}
}
