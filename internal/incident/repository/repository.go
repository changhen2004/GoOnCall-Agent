// Package repository 定义 Incident 持久化接口及其实现。
package repository

import (
	"context"
	"errors"

	"gooncall-agent/internal/incident/model"
)

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("incident not found")

// ErrConflict 表示记录已存在（例如去重冲突）。
var ErrConflict = errors.New("incident already exists")

// ErrConcurrentModification 表示并发修改冲突（乐观锁 version CAS 失败）。
var ErrConcurrentModification = errors.New("state changed concurrently")

// ListFilter 是列表查询的过滤与分页条件。
type ListFilter struct {
	Service string
	Status  model.Status
	Limit   int
	Offset  int
}

// activeIncidentPredicate 是"非终态 Incident"的 SQL 谓词。
//
// 必须与以下两处保持一致：
//   - model.Incident.Fingerprint 上的部分唯一索引 idx_incident_active_fingerprint；
//   - migrations/000004_active_incident_and_events.sql。
const activeIncidentPredicate = "status <> 'RESOLVED' AND status <> 'FAILED' AND status <> 'CANCELLED'"

// Repository 是 Incident 持久化抽象，便于单元测试替换实现。
//
// Incident 状态写入与它的审计事件写入必须原子：CreateWithEvent / UpdateWithEvent
// 在 Postgres 中走同一个事务，Memory 中在同一把锁下先校验后落盘。
// 事件读取走 ListEvents（事件与 Incident 共用同一持久化边界，不需要单独装配事件仓库）。
type Repository interface {
	Create(ctx context.Context, inc *model.Incident) error
	// CreateWithEvent 原子创建 Incident 并写入审计事件（ev 为 nil 时等价于 Create）。
	CreateWithEvent(ctx context.Context, inc *model.Incident, ev *model.IncidentEvent) error
	GetByID(ctx context.Context, id string) (*model.Incident, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*model.Incident, error)
	// GetActiveByFingerprint 返回该指纹当前唯一的非终态 Incident（没有则 ErrNotFound）。
	// 终态历史记录不算命中：同指纹复发必须走新 Incident。
	GetActiveByFingerprint(ctx context.Context, fingerprint string) (*model.Incident, error)
	List(ctx context.Context, filter ListFilter) ([]*model.Incident, error)
	Update(ctx context.Context, inc *model.Incident) error
	// UpdateWithEvent 原子更新 Incident 并写入审计事件（ev 为 nil 时等价于 Update）。
	UpdateWithEvent(ctx context.Context, inc *model.Incident, ev *model.IncidentEvent) error
	// UpdateWithEvents 原子更新 Incident 并写入多条审计事件。
	UpdateWithEvents(ctx context.Context, inc *model.Incident, events []*model.IncidentEvent) error
	// ListEvents 返回某个 Incident 的审计事件，按时间正序。
	ListEvents(ctx context.Context, incidentID string) ([]*model.IncidentEvent, error)
}
