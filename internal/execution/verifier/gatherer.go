package verifier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MetricGatherer 采集处置后的验证指标。
type MetricGatherer interface {
	Gather(ctx context.Context, incidentID string) (Metrics, error)
}

// MockGatherer 返回固定指标（本地 Demo 用）。
type MockGatherer struct {
	Metrics Metrics
}

// NewMockGatherer 返回「已恢复」指标的 Mock 采集器。
func NewMockGatherer() *MockGatherer {
	return &MockGatherer{Metrics: Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001}}
}

// Gather 返回预置指标。
func (g *MockGatherer) Gather(_ context.Context, _ string) (Metrics, error) {
	return g.Metrics, nil
}

// PrometheusGatherer 通过 Prometheus 即时查询采集指标。
type PrometheusGatherer struct {
	client  *http.Client
	baseURL string
	queries Queries
}

// Queries 是验证指标对应的 PromQL（Mode=prometheus 时使用），
// 由配置注入以适配不同业务系统的指标命名。
type Queries struct {
	Consumers  string
	QueueDepth string
	ErrorRate  string
}

// DefaultQueries 返回默认 PromQL（rabbitmq_exporter + http_requests_total 语义）。
func DefaultQueries() Queries {
	return Queries{
		Consumers:  "sum(rabbitmq_queue_consumers)",
		QueueDepth: "sum(rabbitmq_queue_messages_ready)",
		ErrorRate:  `sum(rate(http_requests_total{status=~"5.."}[1m])) / clamp_min(sum(rate(http_requests_total[1m])), 0.001)`,
	}
}

// Prometheus 请求自身的资源上限：调用方 context 可能没有 deadline，
// 不能让它被异常 Prometheus 拖住；响应体只需要解析首条样本，也要有硬上限。
const (
	prometheusTimeout      = 10 * time.Second
	maxPrometheusBodyBytes = 1 << 20 // 1 MiB
)

// NewPrometheusGatherer 创建 Prometheus 采集器；queries 中为空字段回退默认值。
func NewPrometheusGatherer(baseURL string, queries Queries) *PrometheusGatherer {
	def := DefaultQueries()
	if queries.Consumers == "" {
		queries.Consumers = def.Consumers
	}
	if queries.QueueDepth == "" {
		queries.QueueDepth = def.QueueDepth
	}
	if queries.ErrorRate == "" {
		queries.ErrorRate = def.ErrorRate
	}
	return &PrometheusGatherer{
		client:  &http.Client{Timeout: prometheusTimeout},
		baseURL: strings.TrimRight(baseURL, "/"),
		queries: queries,
	}
}

// Gather 查询消费者数、队列积压与 5xx 错误率（PromQL 来自配置）。
func (g *PrometheusGatherer) Gather(ctx context.Context, _ string) (Metrics, error) {
	consumers, err := g.queryScalar(ctx, g.queries.Consumers)
	if err != nil {
		return Metrics{}, err
	}
	depth, err := g.queryScalar(ctx, g.queries.QueueDepth)
	if err != nil {
		return Metrics{}, err
	}
	errorRate, err := g.queryScalar(ctx, g.queries.ErrorRate)
	if err != nil {
		return Metrics{}, err
	}
	return Metrics{
		ConsumerCount: int(consumers),
		QueueDepth:    int(depth),
		ErrorRate:     errorRate,
	}, nil
}

// queryScalar 执行即时查询并返回首个样本值。
func (g *PrometheusGatherer) queryScalar(ctx context.Context, query string) (float64, error) {
	u := g.baseURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPrometheusBodyBytes))
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus status %d: %s", resp.StatusCode, string(body))
	}

	var pr struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Value []interface{} `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return 0, err
	}
	if pr.Status != "success" {
		return 0, fmt.Errorf("prometheus query %q failed: status %q", query, pr.Status)
	}
	if len(pr.Data.Result) == 0 || len(pr.Data.Result[0].Value) < 2 {
		// 空结果不是 0：没有数据就无法证明处置生效，必须让验证失败而不是判成"正常"。
		return 0, fmt.Errorf("prometheus query %q returned no data", query)
	}

	var value float64
	switch v := pr.Data.Result[0].Value[1].(type) {
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fmt.Errorf("parse prometheus value %q of query %q: %w", v, query, err)
		}
		value = parsed
	case float64:
		value = v
	default:
		return 0, fmt.Errorf("unexpected prometheus value type %T for query %q", v, query)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		// NaN/Inf 同样不能按 0 处理：0 会把错误率当成"正常"，从而把未恢复判成已恢复。
		return 0, fmt.Errorf("prometheus query %q returned non-finite value %v", query, value)
	}
	return value, nil
}
