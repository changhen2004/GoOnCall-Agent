package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"gooncall-agent/internal/incident/model"
)

// Postgres 是基于 GORM 的 Incident Repository 实现。
type Postgres struct {
	db *gorm.DB
}

// NewPostgres 创建一个 PostgreSQL Repository。
func NewPostgres(db *gorm.DB) *Postgres {
	return &Postgres{db: db}
}

// AutoMigrate 自动建表（开发便捷；生产环境使用 migrations/）。
func (p *Postgres) AutoMigrate() error {
	return p.db.AutoMigrate(&model.Incident{})
}

// Create 插入 Incident。fingerprint 唯一索引冲突（并发去重竞态）
// 映射为 ErrConflict，由上层按"已存在"处理。
func (p *Postgres) Create(ctx context.Context, inc *model.Incident) error {
	err := p.db.WithContext(ctx).Create(inc).Error
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

// isUniqueViolation 判断是否为 PostgreSQL unique_violation（SQLSTATE 23505）。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func (p *Postgres) GetByID(ctx context.Context, id string) (*model.Incident, error) {
	var inc model.Incident
	err := p.db.WithContext(ctx).First(&inc, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inc, nil
}

func (p *Postgres) GetByFingerprint(ctx context.Context, fingerprint string) (*model.Incident, error) {
	var inc model.Incident
	err := p.db.WithContext(ctx).
		Where("fingerprint = ?", fingerprint).
		Order("created_at DESC").
		First(&inc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &inc, nil
}

func (p *Postgres) List(ctx context.Context, filter ListFilter) ([]*model.Incident, error) {
	q := p.db.WithContext(ctx).Model(&model.Incident{})
	if filter.Service != "" {
		q = q.Where("service = ?", filter.Service)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	q = q.Order("created_at DESC")
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		q = q.Offset(filter.Offset)
	}

	out := make([]*model.Incident, 0)
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Update 以乐观锁（version CAS）更新 Incident：
// 仅当数据库中 version 与传入值一致时更新（version 自增 1），
// 否则返回 ErrConcurrentModification（RowsAffected == 0）。
func (p *Postgres) Update(ctx context.Context, inc *model.Incident) error {
	res := p.db.WithContext(ctx).
		Model(&model.Incident{}).
		Where("id = ? AND version = ?", inc.ID, inc.Version).
		Updates(map[string]any{
			"status":      inc.Status,
			"resolved_at": inc.ResolvedAt,
			"updated_at":  inc.UpdatedAt,
			"version":     gorm.Expr("version + 1"),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrConcurrentModification
	}
	inc.Version++
	return nil
}
