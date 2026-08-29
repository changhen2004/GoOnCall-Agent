package repository

import (
	"context"

	"gooncall-agent/internal/incident/model"
)

// RunRepository 是 Agent Run 生命周期持久化抽象（Run/Step/ToolCall/Evidence）。
type RunRepository interface {
	CreateRun(ctx context.Context, run *model.AgentRun) error
	UpdateRun(ctx context.Context, run *model.AgentRun) error
	GetRun(ctx context.Context, id string) (*model.AgentRun, error)
	ListRunsByIncident(ctx context.Context, incidentID string) ([]*model.AgentRun, error)

	AddStep(ctx context.Context, step *model.AgentStep) error
	ListSteps(ctx context.Context, runID string) ([]*model.AgentStep, error)

	AddToolCall(ctx context.Context, call *model.ToolCall) error
	ListToolCalls(ctx context.Context, runID string) ([]*model.ToolCall, error)

	AddEvidence(ctx context.Context, ev *model.Evidence) error
	ListEvidences(ctx context.Context, runID string) ([]*model.Evidence, error)
}
