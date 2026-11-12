package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPrometheusQueryTruncatesSeries(t *testing.T) {
	series := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		series = append(series, fmt.Sprintf(`{"metric":{"i":"%d"},"value":[1700000000,"1"]}`, i))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`,
			strings.Join(series, ","))
	}))
	defer server.Close()

	tool := NewPrometheusTools(PrometheusOptions{URL: server.URL, Timeout: time.Second, MaxSeries: 5})[0]
	out, err := tool.Run(context.Background(), json.RawMessage(`{"query":"up"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var response struct {
		Result      []any `json:"result"`
		ResultCount int   `json:"result_count"`
		Truncated   bool  `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result) != 5 || response.ResultCount != 60 || !response.Truncated {
		t.Fatalf("truncation = %+v", response)
	}
}

func TestPrometheusValidationAndUnavailable(t *testing.T) {
	tool := NewPrometheusTools(PrometheusOptions{URL: "http://127.0.0.1:1", Timeout: 50 * time.Millisecond})[0]
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"query":""}`)); err == nil {
		t.Fatal("空 query 应失败")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"query":"up"}`)); err == nil ||
		!strings.Contains(err.Error(), "不可达") {
		t.Fatalf("不可达错误 = %v", err)
	}

	rangeTool := NewPrometheusTools(PrometheusOptions{URL: "http://127.0.0.1:1"})[1]
	if _, err := rangeTool.Run(context.Background(),
		json.RawMessage(`{"query":"up","start":"2024-01-01T00:00:00Z","end":"2024-01-03T00:00:00Z"}`)); err == nil ||
		!strings.Contains(err.Error(), "24 小时") {
		t.Fatalf("时间范围错误 = %v", err)
	}
}

func TestPrometheusTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"status":"success","data":{"result":[]}}`))
	}))
	defer server.Close()

	tool := NewPrometheusTools(PrometheusOptions{URL: server.URL, Timeout: 30 * time.Millisecond})[0]
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"query":"up"}`)); err == nil {
		t.Fatal("超时应失败")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Run(ctx, json.RawMessage(`{"query":"up"}`)); err == nil {
		t.Fatal("取消应失败")
	}
}

func TestPrometheusAlertsTruncates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"alerts":[
			{"labels":{"alertname":"A"},"annotations":{},"state":"firing"},
			{"labels":{"alertname":"B"},"annotations":{},"state":"firing"},
			{"labels":{"alertname":"C"},"annotations":{},"state":"firing"}
		]}}`)
	}))
	defer server.Close()

	tool := NewPrometheusTools(PrometheusOptions{URL: server.URL, MaxAlerts: 2})[2]
	out, err := tool.Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var response struct {
		Alerts      []any `json:"alerts"`
		ResultCount int   `json:"result_count"`
		Truncated   bool  `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Alerts) != 2 || response.ResultCount != 3 || !response.Truncated {
		t.Fatalf("alerts = %+v", response)
	}
}
