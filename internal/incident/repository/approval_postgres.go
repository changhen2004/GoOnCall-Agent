package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"gooncall-agent/internal/incident/model"
)

// PostgresApproval 是基于 GORM 的 ApprovalRepository 实现。
type PostgresApproval struct {
	db *gorm.DB
}

// NewPostgresApproval 创建 PostgreSQL 审批仓库。
func NewPostgresApproval(db *gorm.DB) *PostgresApproval {
	return &PostgresApproval{db: db}
}

func (p *PostgresApproval) Create(ctx context.Context, a *model.Approval) error {
	return p.db.WithContext(ctx).Create(a).Error
}

func (p *PostgresApproval) Get(ctx context.Context, id string) (*model.Approval, error) {
	var a model.Approval
	err := p.db.WithContext(ctx).First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (p *PostgresApproval) ListByRun(ctx context.Context, runID string) ([]*model.Approval, error) {
	out := make([]*model.Approval, 0)
	err := p.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at ASC, id ASC").Find(&out).Error
	return out, err
}

func (p *PostgresApproval) ListPending(ctx context.Context) ([]*model.Approval, error) {
	out := make([]*model.Approval, 0)
	err := p.db.WithContext(ctx).Where("status = ?", model.ApprovalPending).Order("created_at ASC, id ASC").Find(&out).Error
	return out, err
}

// TransitionStatus 原子条件更新：先读快照，再用 WHERE id = ? AND status = from 落库，
// RowsAffected == 0 即状态已被其他请求改写（并发批准/已拒绝/已过期）-> ErrConflict。
// 条件里带 status 保证只有第一个请求能从 from 迁移，不需要显式事务或行锁。
func (p *PostgresApproval) TransitionStatus(ctx context.Context, id string, from, to model.ApprovalStatus, mutate func(*model.Approval)) (*model.Approval, error) {
	a, err := p.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Status != from {
		return nil, ErrConflict
	}
	a.Status = to
	a.UpdatedAt = time.Now()
	if mutate != nil {
		mutate(a)
	}

	res := p.db.WithContext(ctx).Model(&model.Approval{}).
		Where("id = ? AND status = ?", id, from).
		Updates(map[string]any{
			"status":       a.Status,
			"approved_by":  a.ApprovedBy,
			"approved_at":  a.ApprovedAt,
			"result":       a.Result,
			"error":        a.Error,
			"operation_id": a.OperationID,
			"updated_at":   a.UpdatedAt,
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrConflict
	}
	return a, nil
}

// ExpirePendingByIncident 把该 Incident 下所有 PENDING 审批置为 EXPIRED，返回条数。
func (p *PostgresApproval) ExpirePendingByIncident(ctx context.Context, incidentID string) (int, error) {
	if incidentID == "" {
		// 空 incident_id 不能匹配任何审批（避免误伤历史/未绑定记录）。
		return 0, nil
	}
	res := p.db.WithContext(ctx).Model(&model.Approval{}).
		Where("incident_id = ? AND status = ?", incidentID, model.ApprovalPending).
		Updates(map[string]any{
			"status":     model.ApprovalExpired,
			"error":      "incident resolved externally",
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
}
