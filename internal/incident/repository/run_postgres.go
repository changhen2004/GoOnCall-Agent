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

// CreateRun 原子创建 Run：主键重复或同一 Incident 已有 RUNNING/COMPLETED Run
// （部分唯一索引 idx_agent_run_active 冲突）时返回 ErrConflict。
func (p *PostgresRun) CreateRun(ctx context.Context, run *model.AgentRun) error {
	err := p.db.WithContext(ctx).Create(run).Error
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// UpdateRun 显式 UPDATE ... WHERE id = ? 更新 Run：
//
//   - 命中 0 行说明 Run 不存在 -> ErrNotFound（不能用 GORM Save：它在 UPDATE 命中 0 行时
//     会退化成 OnConflict(UpdateAll) 的 INSERT，把从未创建或已删除的 Run "复活"）；
//   - 把状态改成 RUNNING/COMPLETED 时若同一 Incident 已有其他活动 Run，
//     部分唯一索引 idx_agent_run_active 冲突统一映射为 ErrConflict（与 Memory 语义一致）。
//
// 用 map 更新而不是 struct：零值/NULL 字段（finished_at、error）必须能真正写回。
func (p *PostgresRun) UpdateRun(ctx context.Context, run *model.AgentRun) error {
	res := p.db.WithContext(ctx).
		Model(&model.AgentRun{}).
		Where("id = ?", run.ID).
		Updates(map[string]any{
			"incident_id":  run.IncidentID,
			"agent_type":   run.AgentType,
			"status":       run.Status,
			"goal":         run.Goal,
			"current_step": run.CurrentStep,
			"started_at":   run.StartedAt,
			"finished_at":  run.FinishedAt,
			"error":        run.Error,
		})
	if res.Error != nil {
		if isUniqueViolation(res.Error) {
			return ErrConflict
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
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

// ListRunsByIncident 按 StartedAt 倒序；时间并列用 id 正序兜底（与 Memory 语义一致）。
func (p *PostgresRun) ListRunsByIncident(ctx context.Context, incidentID string) ([]*model.AgentRun, error) {
	out := make([]*model.AgentRun, 0)
	err := p.db.WithContext(ctx).
		Where("incident_id = ?", incidentID).
		Order("started_at DESC, id ASC").
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

// UpdateToolCall 补写工具结果；命中 0 行说明记录不存在（不能用 Save：会退化成 INSERT）。
func (p *PostgresRun) UpdateToolCall(ctx context.Context, call *model.ToolCall) error {
	res := p.db.WithContext(ctx).
		Model(&model.ToolCall{}).
		Where("id = ?", call.ID).
		Updates(map[string]any{
			"result":      call.Result,
			"status":      call.Status,
			"duration_ms": call.DurationMs,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListToolCalls 按 CreatedAt 正序；时间并列用 id 正序兜底（与 Memory 语义一致）。
func (p *PostgresRun) ListToolCalls(ctx context.Context, runID string) ([]*model.ToolCall, error) {
	out := make([]*model.ToolCall, 0)
	err := p.db.WithContext(ctx).Where("run_id = ?", runID).Order("created_at ASC, id ASC").Find(&out).Error
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
