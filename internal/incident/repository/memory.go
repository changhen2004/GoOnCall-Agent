package repository

import (
	"context"
	"sort"
	"sync"

	"gooncall-agent/internal/incident/model"
)

// Memory 是线程安全的内存 Repository，用于单元测试与本地开发。
//
// Incident 与它的审计事件放在同一个仓库、同一把锁下，保证 CreateWithEvent /
// UpdateWithEvent 的"要么都写、要么都不写"语义与 Postgres 事务一致。
type Memory struct {
	mu     sync.RWMutex
	items  map[string]*model.Incident
	events map[string][]*model.IncidentEvent
}

// NewMemory 创建一个空的内存 Repository。
func NewMemory() *Memory {
	return &Memory{
		items:  make(map[string]*model.Incident),
		events: make(map[string][]*model.IncidentEvent),
	}
}

// Create 插入 Incident。ID 重复、或同一 fingerprint 已存在**非终态**记录时返回 ErrConflict
// （与 Postgres 上的部分唯一索引 idx_incident_active_fingerprint 语义一致：
// 终态历史不阻止同指纹创建新 Incident）。
func (m *Memory) Create(ctx context.Context, inc *model.Incident) error {
	return m.CreateWithEvent(ctx, inc, nil)
}

// createLocked 校验并写入 Incident；调用方必须持有写锁。
func (m *Memory) createLocked(inc *model.Incident) error {
	if _, ok := m.items[inc.ID]; ok {
		return ErrConflict
	}
	for _, existing := range m.items {
		if existing.Fingerprint != "" && existing.Fingerprint == inc.Fingerprint && !existing.Status.IsTerminal() {
			return ErrConflict
		}
	}
	cp := *inc
	m.items[inc.ID] = &cp
	return nil
}

func (m *Memory) GetByID(_ context.Context, id string) (*model.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inc, ok := m.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *inc
	return &cp, nil
}

func (m *Memory) GetByFingerprint(_ context.Context, fingerprint string) (*model.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var found *model.Incident
	for _, inc := range m.items {
		if inc.Fingerprint != fingerprint {
			continue
		}
		cp := *inc
		if found == nil || cp.CreatedAt.After(found.CreatedAt) {
			found = &cp
		}
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

// GetActiveByFingerprint 返回该指纹当前唯一的非终态 Incident（没有则 ErrNotFound）。
func (m *Memory) GetActiveByFingerprint(_ context.Context, fingerprint string) (*model.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var found *model.Incident
	for _, inc := range m.items {
		if inc.Fingerprint != fingerprint || inc.Status.IsTerminal() {
			continue
		}
		cp := *inc
		if found == nil || cp.CreatedAt.After(found.CreatedAt) {
			found = &cp
		}
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, nil
}

func (m *Memory) List(_ context.Context, filter ListFilter) ([]*model.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*model.Incident, 0, len(m.items))
	for _, inc := range m.items {
		if filter.Service != "" && inc.Service != filter.Service {
			continue
		}
		if filter.Status != "" && inc.Status != filter.Status {
			continue
		}
		cp := *inc
		out = append(out, &cp)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })

	if filter.Offset > 0 {
		if filter.Offset >= len(out) {
			return []*model.Incident{}, nil
		}
		out = out[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(out) {
		out = out[:filter.Limit]
	}
	return out, nil
}

// Update 以乐观锁（version CAS）更新 Incident：
// 仅当存储中 version 与传入值一致时更新（version 自增 1），
// 否则返回 ErrConcurrentModification。
func (m *Memory) Update(ctx context.Context, inc *model.Incident) error {
	return m.UpdateWithEvent(ctx, inc, nil)
}

// updateLocked 校验并写入 Incident（存储中的 version 自增 1）；调用方必须持有写锁。
// 注意：这里必须先把新版本写进存储，调用方再自增入参的 inc.Version。
func (m *Memory) updateLocked(inc *model.Incident) error {
	cur, ok := m.items[inc.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Version != inc.Version {
		return ErrConcurrentModification
	}
	cp := *inc
	cp.Version = inc.Version + 1
	m.items[inc.ID] = &cp
	return nil
}
