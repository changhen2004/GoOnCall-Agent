package repository

import (
	"context"
	"errors"

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

// AutoMigrate 自动建表。
func (p *PostgresApproval) AutoMigrate() error {
	return p.db.AutoMigrate(&model.Approval{})
}

func (p *PostgresApproval) Create(ctx context.Context, a *model.Approval) error {
	return p.db.WithContext(ctx).Create(a).Error
}

func (p *PostgresApproval) Update(ctx context.Context, a *model.Approval) error {
	return p.db.WithContext(ctx).Save(a).Error
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
	err := p.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at").Find(&out).Error
	return out, err
}

func (p *PostgresApproval) ListPending(ctx context.Context) ([]*model.Approval, error) {
	out := make([]*model.Approval, 0)
	err := p.db.WithContext(ctx).Where("status = ?", model.ApprovalPending).Order("created_at").Find(&out).Error
	return out, err
}
