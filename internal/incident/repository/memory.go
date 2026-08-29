package repository

import (
	"context"
	"sort"
	"sync"

	"gooncall-agent/internal/incident/model"
)

// Memory 是线程安全的内存 Repository，用于单元测试与本地开发。
type Memory struct {
	mu    sync.RWMutex
	items map[string]*model.Incident
}

// NewMemory 创建一个空的内存 Repository。
func NewMemory() *Memory {
	return &Memory{items: make(map[string]*model.Incident)}
}

func (m *Memory) Create(_ context.Context, inc *model.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[inc.ID]; ok {
		return ErrConflict
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
	for _, inc := range m.items {
		if inc.Fingerprint == fingerprint {
			cp := *inc
			return &cp, nil
		}
	}
	return nil, ErrNotFound
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

func (m *Memory) Update(_ context.Context, inc *model.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[inc.ID]; !ok {
		return ErrNotFound
	}
	cp := *inc
	m.items[inc.ID] = &cp
	return nil
}
