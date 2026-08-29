package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"gooncall-agent/internal/incident/model"
)

// PostgresRun 是基于 GORM 的 RunRepository 实现。
type PostgresRun struct {
	db *gorm.DB
}

// NewPostgresRun 创建 PostgreSQL RunRepository。
func NewPostgresRun(db *gorm.DB) *PostgresRun {
	return &PostgresRun{db: db}
}

// AutoMigrate 自动建表。
func (p *PostgresRun) AutoMigrate() error {
	return p.db.AutoMigrate(&model.AgentRun{}, &model.AgentStep{}, &model.ToolCall{}, &model.Evidence{})
}

func (p *PostgresRun) CreateRun(ctx context.Context, run *model.AgentRun) error {
	return p.db.WithContext(ctx).Create(run).Error
}

func (p *PostgresRun) UpdateRun(ctx context.Context, run *model.AgentRun) error {
	return p.db.WithContext(ctx).Save(run).Error
}

func (p *PostgresRun) GetRun(ctx context.Context, id string) (*model.AgentRun, error) {
	var run model.AgentRun
	err := p.db.WithContext(ctx).First(&run, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (p *PostgresRun) ListRunsByIncident(ctx context.Context, incidentID string) ([]*model.AgentRun, error) {
	out := make([]*model.AgentRun, 0)
	err := p.db.WithContext(ctx).
		Where("incident_id = ?", incidentID).
		Order("started_at DESC").
		Find(&out).Error
	return out, err
}

func (p *PostgresRun) AddStep(ctx context.Context, step *model.AgentStep) error {
	return p.db.WithContext(ctx).Create(step).Error
}

func (p *PostgresRun) ListSteps(ctx context.Context, runID string) ([]*model.AgentStep, error) {
	out := make([]*model.AgentStep, 0)
	err := p.db.WithContext(ctx).Where("run_id = ?", runID).Order("step_index").Find(&out).Error
	return out, err
}

func (p *PostgresRun) AddToolCall(ctx context.Context, call *model.ToolCall) error {
	return p.db.WithContext(ctx).Create(call).Error
}

func (p *PostgresRun) ListToolCalls(ctx context.Context, runID string) ([]*model.ToolCall, error) {
	out := make([]*model.ToolCall, 0)
	err := p.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at").Find(&out).Error
	return out, err
}

func (p *PostgresRun) AddEvidence(ctx context.Context, ev *model.Evidence) error {
	return p.db.WithContext(ctx).Create(ev).Error
}

func (p *PostgresRun) ListEvidences(ctx context.Context, runID string) ([]*model.Evidence, error) {
	out := make([]*model.Evidence, 0)
	err := p.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at").Find(&out).Error
	return out, err
}
