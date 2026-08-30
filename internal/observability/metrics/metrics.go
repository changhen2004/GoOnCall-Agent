// Package metrics 提供 GoOnCall 自身的 Prometheus 指标（设计文档 21）。
package metrics

import (
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
)
