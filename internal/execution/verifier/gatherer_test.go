package verifier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMockGatherer(t *testing.T) {
	g := NewMockGatherer()
	m, err := g.Gather(context.Background(), "inc_1")
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if m.ConsumerCount != 5 || m.QueueDepth != 100 || m.ErrorRate != 0.001 {
		t.Fatalf("metrics = %+v", m)
	}
}

func TestPrometheusGatherer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		val := "0"
		switch q {
		case "sum(rabbitmq_queue_consumers)":
			val = "3"
		case "sum(rabbitmq_queue_messages_ready)":
			val = "50"
		default:
			val = "0.002"
		}
		resp := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []any{
					map[string]any{
						"metric": map[string]string{},
						"value":  []any{1700000000, val},
					},
				},
			},
		}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	g := NewPrometheusGatherer(server.URL, DefaultQueries())
	m, err := g.Gather(context.Background(), "inc_1")
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if m.ConsumerCount != 3 || m.QueueDepth != 50 || m.ErrorRate != 0.002 {
		t.Fatalf("metrics = %+v", m)
	}
}

// TestPrometheusGatherer_CustomQueries 验证自定义 PromQL（对接业务系统指标名，如 GoCommunity）。
func TestPrometheusGatherer_CustomQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		val := "0"
		switch q {
		case `sum(resource_community_rabbitmq_consumers)`:
			val = "2"
		case `sum(resource_community_rabbitmq_messages_ready)`:
			val = "5"
		default:
			val = "0.004"
		}
		resp := map[string]any{
			"status": "success",
			"data": map[string]any{
				"resultType": "vector",
				"result": []any{
					map[string]any{"metric": map[string]string{}, "value": []any{1700000000, val}},
				},
			},
		}
		data, _ := json.Marshal(resp)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	g := NewPrometheusGatherer(server.URL, Queries{
		Consumers:  `sum(resource_community_rabbitmq_consumers)`,
		QueueDepth: `sum(resource_community_rabbitmq_messages_ready)`,
		ErrorRate:  `sum(rate(resource_community_http_requests_total{status=~"5.."}[1m]))`,
	})
	m, err := g.Gather(context.Background(), "inc_1")
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if m.ConsumerCount != 2 || m.QueueDepth != 5 || m.ErrorRate != 0.004 {
		t.Fatalf("metrics = %+v", m)
	}
}

// TestPrometheusGatherer_EmptyQueriesFallsBack 验证空 PromQL 字段回退默认值。
func TestPrometheusGatherer_EmptyQueriesFallsBack(t *testing.T) {
	g := NewPrometheusGatherer("http://example.com", Queries{})
	if g.queries.Consumers == "" || g.queries.QueueDepth == "" || g.queries.ErrorRate == "" {
		t.Fatalf("empty queries should fall back to defaults, got %+v", g.queries)
	}
}

// TestPrometheusGatherer_HasOwnTimeout 调用方 context 可能没有 deadline，
// 客户端必须自带超时，否则异常 Prometheus 能长期占住验证链路。
func TestPrometheusGatherer_HasOwnTimeout(t *testing.T) {
	g := NewPrometheusGatherer("http://example.com", DefaultQueries())
	if g.client.Timeout != prometheusTimeout {
		t.Fatalf("client timeout = %v, want %v", g.client.Timeout, prometheusTimeout)
	}
}

// TestPrometheusGatherer_OversizedBodyIsBounded 超过上限的响应体只读取固定字节数，不无界读内存。
func TestPrometheusGatherer_OversizedBodyIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"3"]}]}}`))
		// 之后填充远超上限的空白：截断在 JSON 之后，解析仍然成功且不读满整个响应。
		padding := make([]byte, 2<<20)
		for i := range padding {
			padding[i] = ' '
		}
		_, _ = w.Write(padding)
	}))
	defer server.Close()

	g := NewPrometheusGatherer(server.URL, DefaultQueries())
	m, err := g.Gather(context.Background(), "inc_1")
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if m.ConsumerCount != 3 {
		t.Fatalf("metrics = %+v, want the first sample parsed", m)
	}
}

// TestPrometheusGatherer_NoData 空结果必须报错：没有数据不能当作 0（0 会被判成"已恢复"）。
func TestPrometheusGatherer_NoData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	g := NewPrometheusGatherer(server.URL, DefaultQueries())
	if _, err := g.Gather(context.Background(), "inc_1"); err == nil {
		t.Fatal("Gather() with empty result should fail, got nil error")
	}
}

// TestPrometheusGatherer_NonFinite 覆盖 NaN / Inf：同样不能按 0 处理。
func TestPrometheusGatherer_NonFinite(t *testing.T) {
	for _, raw := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(raw, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resp := map[string]any{
					"status": "success",
					"data": map[string]any{
						"resultType": "vector",
						"result": []any{
							map[string]any{"metric": map[string]string{}, "value": []any{1700000000, raw}},
						},
					},
				}
				data, _ := json.Marshal(resp)
				_, _ = w.Write(data)
			}))
			defer server.Close()

			g := NewPrometheusGatherer(server.URL, DefaultQueries())
			if _, err := g.Gather(context.Background(), "inc_1"); err == nil {
				t.Fatalf("Gather() with %s should fail, got nil error", raw)
			}
		})
	}
}

// TestPrometheusGatherer_StatusNotSuccess 覆盖 status != success。
func TestPrometheusGatherer_StatusNotSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	g := NewPrometheusGatherer(server.URL, DefaultQueries())
	if _, err := g.Gather(context.Background(), "inc_1"); err == nil {
		t.Fatal("Gather() with status=error should fail, got nil error")
	}
}
