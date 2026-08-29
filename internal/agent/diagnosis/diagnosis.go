// Package diagnosis 提供诊断 Agent（ReAct）：持续收集证据、定位根因。
package diagnosis

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	incidentmodel "gooncall-agent/internal/incident/model"
)

// Agent 是诊断 Agent。
type Agent struct {
	model    model.ToolCallingChatModel
	maxSteps int
}

// New 创建诊断 Agent。
func New(m model.ToolCallingChatModel, maxSteps int) *Agent {
	return &Agent{model: m, maxSteps: maxSteps}
}

// Build 构建 ReAct Agent。
func (a *Agent) Build(ctx context.Context) (*react.Agent, error) {
	return react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: a.model,
		MaxStep:          a.maxSteps,
	})
}

// BuildMessages 构造诊断输入消息：系统提示词 + 事件上下文。
func BuildMessages(systemPrompt string, inc *incidentmodel.Incident) []*schema.Message {
	ctx, _ := json.Marshal(map[string]any{
		"service":     inc.Service,
		"severity":    inc.Severity,
		"title":       inc.Title,
		"description": inc.Description,
		"alert_name":  inc.AlertName,
		"status":      string(inc.Status),
	})
	return []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{Role: schema.User, Content: "请诊断以下运维事件，优先使用工具收集证据，不要凭空猜测：\n" + string(ctx)},
	}
}
