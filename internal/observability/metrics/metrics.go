// Package metrics 提供 GoOnCall 自身的 Prometheus 指标（设计文档 21）。
package metrics

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// IncidentsTotal 统计 Incident 创建（按 service/severity）。
	IncidentsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gooncall_incidents_total",
		Help: "Total number of incidents created.",
	}, []string{"service", "severity"})

	// AgentRunsTotal 统计 Agent Run（按 agent_type/status）。
	AgentRunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gooncall_agent_runs_total",
		Help: "Total number of agent runs.",
	}, []string{"agent_type", "status"})

	// ToolCallsTotal 统计工具调用（按 tool/status）。
	ToolCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gooncall_tool_calls_total",
		Help: "Total number of tool calls.",
	}, []string{"tool", "status"})

	// ApprovalsTotal 统计审批（按 action/status）。
	ApprovalsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gooncall_approval_total",
		Help: "Total number of approvals.",
	}, []string{"action", "status"})

	// WebhookTotal 统计 Alertmanager webhook 处理结果（按 action=create/resolve, status=ok/error）。
	WebhookTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gooncall_webhook_total",
		Help: "Total number of alertmanager webhook events processed.",
	}, []string{"action", "status"})

	// RetriesTotal 统计消费失败转入重试队列的次数（未进入 DLQ）。
	RetriesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gooncall_message_retries_total",
		Help: "Total number of agent event handling failures routed back to the retry queue.",
	})

	// DLQTotal 统计重试耗尽并已确认进入死信队列的消息数。
	DLQTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "gooncall_message_dlq_total",
		Help: "Total number of agent events that exhausted retries and were confirmed into the DLQ.",
	})

	// ExecutionsTotal 统计处置执行结果（completed / failed / unknown）。
	ExecutionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gooncall_executions_total",
		Help: "Total number of remediation executions by action and outcome.",
	}, []string{"action", "result"})

	// VerificationsTotal 统计处置后验证结果（verified / verify_failed）。
	VerificationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "gooncall_verifications_total",
		Help: "Total number of post-remediation verification outcomes by action and result.",
	}, []string{"action", "result"})

	// ClosureDurationSeconds 统计从 Incident 开始到 RESOLVED 的闭环耗时。
	ClosureDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gooncall_closure_duration_seconds",
		Help:    "Time from incident start to RESOLVED, in seconds.",
		Buckets: []float64{5, 15, 30, 60, 120, 300, 600, 1800, 3600},
	}, []string{"action"})
)

// StatusCollector 在 Prometheus 抓取时按状态统计资源数量。
//
// 用采集器而不是后台轮询：指标总是最新的，也不需要额外的 goroutine 与定时器。
//
// ponytail: 每次抓取做一次全量 List；Incident/审批量级到万级时改成 SQL GROUP BY 计数。
type StatusCollector struct {
	desc  *prometheus.Desc
	count func(ctx context.Context) (map[string]int, error)
}

// NewStatusCollector 创建状态计数采集器：name 为指标名，label 为状态标签名。
func NewStatusCollector(name, help, label string, count func(context.Context) (map[string]int, error)) *StatusCollector {
	return &StatusCollector{
		desc:  prometheus.NewDesc(name, help, []string{label}, nil),
		count: count,
	}
}

// Describe 实现 prometheus.Collector。
func (c *StatusCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

// Collect 实现 prometheus.Collector：单个状态没有样本时不输出该标签。
func (c *StatusCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), collectorTimeout)
	defer cancel()

	counts, err := c.count(ctx)
	if err != nil {
		// 抓取时数据库不可用：不输出样本（缺数据比伪造 0 更诚实），并记录原因。
		slog.Warn("collect status counts failed", "metric", c.desc.String(), "error", err)
		return
	}
	for status, n := range counts {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(n), status)
	}
}

// collectorTimeout 是单次抓取内查询超时。
const collectorTimeout = 5 * time.Second

// Register 注册采集器；重复注册（例如测试里多次 Bootstrap）不报错。
func Register(c prometheus.Collector) error {
	if err := prometheus.Register(c); err != nil {
		var already prometheus.AlreadyRegisteredError
		if errors.As(err, &already) {
			return nil
		}
		return err
	}
	return nil
}
