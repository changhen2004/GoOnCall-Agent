package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"gooncall-agent/internal/incident/model"
)

// CreateWithEvent 在同一个 PostgreSQL 事务中写入 Incident 与审计事件：
// 任一步失败整体回滚，不会出现"状态已提交但接口返回失败、审计事件永久缺失"。
// ev 为 nil 时等价于 Create。
func (p *Postgres) CreateWithEvent(ctx context.Context, inc *model.Incident, ev *model.IncidentEvent) error {
	if ev == nil {
		return p.Create(ctx, inc)
	}
	var incErr error
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(inc).Error; e != nil {
			incErr = e
			return e
		}
		return tx.Create(ev).Error
	})
	switch {
	case incErr != nil && isUniqueViolation(incErr):
		// 活动 fingerprint 冲突：交给上层按"已存在"去重。
		return ErrConflict
	case incErr != nil:
		return incErr
	case err != nil:
		return fmt.Errorf("write incident event for %s: %w", inc.ID, err)
	}
	return nil
}

// UpdateWithEvent 在同一个事务中做 version CAS 更新并写入审计事件，任一步失败整体回滚。
// ev 为 nil 时等价于 Update。
func (p *Postgres) UpdateWithEvent(ctx context.Context, inc *model.Incident, ev *model.IncidentEvent) error {
	if ev == nil {
		return p.Update(ctx, inc)
	}
	var updErr error
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := p.updateIncident(tx, inc); e != nil {
			updErr = e
			return e
		}
		return tx.Create(ev).Error
	})
	if updErr != nil {
		return updErr
	}
	if err != nil {
		return fmt.Errorf("write incident event for %s: %w", inc.ID, err)
	}
	inc.Version++
	return nil
}

// UpdateWithEvents 在同一个事务中做 version CAS 更新并写入多条审计事件。
func (p *Postgres) UpdateWithEvents(ctx context.Context, inc *model.Incident, events []*model.IncidentEvent) error {
	var updErr error
	err := p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := p.updateIncident(tx, inc); e != nil {
			updErr = e
			return e
		}
		if len(events) == 0 {
			return nil
		}
		return tx.Create(&events).Error
	})
	if updErr != nil {
		return updErr
	}
	if err != nil {
		return fmt.Errorf("write incident events for %s: %w", inc.ID, err)
	}
	inc.Version++
	return nil
}

// ListEvents 返回某 Incident 的事件，按创建时间正序（同一时间以 ID 兜底保证稳定）。
func (p *Postgres) ListEvents(ctx context.Context, incidentID string) ([]*model.IncidentEvent, error) {
	out := make([]*model.IncidentEvent, 0)
	err := p.db.WithContext(ctx).
		Where("incident_id = ?", incidentID).
		Order("created_at ASC, id ASC").
		Find(&out).Error
	return out, err
}
