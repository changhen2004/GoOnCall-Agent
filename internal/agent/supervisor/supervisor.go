// Package supervisor 是事件协调 Agent（Supervisor）。
//
// Phase 2 仅定义协调角色与系统提示词；完整的「Supervisor 调度 Diagnosis / Action / Review」
// 多 Agent 编排在 Phase 4 实现（届时将引入 Eino MultiAgent / Graph）。
package supervisor

import (
	"github.com/cloudwego/eino/components/model"
)

// Agent 是 Supervisor Agent（占位）。
type Agent struct {
	model model.ToolCallingChatModel
}

// New 创建 Supervisor Agent。
func New(m model.ToolCallingChatModel) *Agent {
	return &Agent{model: m}
}

// Model 返回底层模型。
func (a *Agent) Model() model.ToolCallingChatModel {
	return a.model
}
