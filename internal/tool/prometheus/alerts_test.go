package prometheus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlerts_SuccessFiltersFiring(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/alerts" {
			t.Errorf("path = %s, want /api/v1/alerts", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"alerts":[
			{"labels":{"alertname":"HighErrorRate","severity":"critical","service":"gocommunity","job":"x"},"annotations":{"summary":"err"},"state":"firing","value":"1e+00","activeAt":"2026-08-30T00:00:00Z"},
			{"labels":{"alertname":"HighLatency","severity":"warning","job":"x"},"annotations":{},"state":"pending","value":"0"}
		]}}`))
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, err := tool.AlertsEinoTool()
	if err != nil {
		t.Fatalf("AlertsEinoTool() error = %v", err)
	}

	out, err := einoTool.InvokableRun(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	// 默认仅返回 firing：只有 HighErrorRate。
	if !strings.Contains(out, "HighErrorRate") || strings.Contains(out, "HighLatency") {
		t.Fatalf("unexpected output: %s", out)
	}
	// service 解析：优先 labels.service，其次 job。
	if !strings.Contains(out, `"service":"gocommunity"`) {
		t.Fatalf("service should come from labels.service: %s", out)
	}
}

func TestAlerts_IncludeInactive(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"success","data":{"alerts":[
			{"labels":{"alertname":"A"},"annotations":{},"state":"firing","value":"1"},
			{"labels":{"alertname":"B"},"annotations":{},"state":"pending","value":"0"}
		]}}`))
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, _ := tool.AlertsEinoTool()

	out, err := einoTool.InvokableRun(context.Background(), `{"include_inactive":true}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, "A") || !strings.Contains(out, "B") {
		t.Fatalf("include_inactive should return pending alerts too: %s", out)
	}
}

func TestAlerts_NonSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"error","errorType":"internal","error":"boom"}`))
	}))
	defer server.Close()

	tool := New(server.URL)
	einoTool, _ := tool.AlertsEinoTool()

	if _, err := einoTool.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("expected error for non-success alerts response")
	}
}
