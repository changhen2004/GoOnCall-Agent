package repository

import (
	"context"

	"gooncall-agent/internal/incident/model"
)

// RunRepository 是 Agent Run 生命周期持久化抽象（Run/Step/ToolCall/Evidence）。
type RunRepository interface {
	// CreateRun 原子创建 Run：同一 Incident 已存在 RUNNING/COMPLETED Run 时返回 ErrConflict
	// （由数据库部分唯一索引 idx_agent_run_active 保证，Memory 实现语义一致）。
	// 调用方据此把重复消息当作已处理成功返回；Run 落为 FAILED/INTERRUPTED 后允许重试创建新 Run。
	CreateRun(ctx context.Context, run *model.AgentRun) error
	UpdateRun(ctx context.Context, run *model.AgentRun) error
	GetRun(ctx context.Context, id string) (*model.AgentRun, error)
	ListRunsByIncident(ctx context.Context, incidentID string) ([]*model.AgentRun, error)

	AddStep(ctx context.Context, step *model.AgentStep) error
	ListSteps(ctx context.Context, runID string) ([]*model.AgentStep, error)

	AddToolCall(ctx context.Context, call *model.ToolCall) error
	// UpdateToolCall 在工具返回后补写结果字段（result / status / duration_ms）。
	// 调用方先 AddToolCall 写 RUNNING 记录，保证工具内创建的 Approval 引用的 ToolCall 已经存在。
	// 记录不存在返回 ErrNotFound。
	UpdateToolCall(ctx context.Context, call *model.ToolCall) error
	ListToolCalls(ctx context.Context, runID string) ([]*model.ToolCall, error)

	AddEvidence(ctx context.Context, ev *model.Evidence) error
	ListEvidences(ctx context.Context, runID string) ([]*model.Evidence, error)
}
