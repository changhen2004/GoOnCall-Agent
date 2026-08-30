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

func TestPrometheusGatherer_NoData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer server.Close()

	g := NewPrometheusGatherer(server.URL, DefaultQueries())
	m, err := g.Gather(context.Background(), "inc_1")
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	if m.ConsumerCount != 0 || m.QueueDepth != 0 || m.ErrorRate != 0 {
		t.Fatalf("metrics = %+v, want all zero", m)
	}
}
