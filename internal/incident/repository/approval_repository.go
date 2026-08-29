package repository

import (
	"context"

	"gooncall-agent/internal/incident/model"
)

// ApprovalRepository 是审批持久化抽象。
type ApprovalRepository interface {
	Create(ctx context.Context, a *model.Approval) error
	Update(ctx context.Context, a *model.Approval) error
	Get(ctx context.Context, id string) (*model.Approval, error)
	ListByRun(ctx context.Context, runID string) ([]*model.Approval, error)
	ListPending(ctx context.Context) ([]*model.Approval, error)
}
