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

// Repository 是 Incident 持久化抽象，便于单元测试替换实现。
type Repository interface {
	Create(ctx context.Context, inc *model.Incident) error
	GetByID(ctx context.Context, id string) (*model.Incident, error)
	GetByFingerprint(ctx context.Context, fingerprint string) (*model.Incident, error)
	List(ctx context.Context, filter ListFilter) ([]*model.Incident, error)
	Update(ctx context.Context, inc *model.Incident) error
}
