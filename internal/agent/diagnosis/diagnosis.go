// Package diagnosis 提供诊断 Agent（ReAct）：持续收集证据、定位根因。
package diagnosis

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
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
//
// 注意：工具必须在这里放入 ToolsConfig —— react.NewAgent 在构建时用
// ToolsConfig 生成 ToolInfo 并通过 model.WithTools 绑定到模型；如果只在
// Generate 时传 react.WithTools，LLM 请求中不会包含工具定义，模型会误以为
// 没有任何工具可用（表现为拒绝调用工具的纯文本回复）。
func (a *Agent) Build(ctx context.Context, tools []tool.BaseTool) (*react.Agent, error) {
	return react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: a.model,
		MaxStep:          a.maxSteps,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
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
