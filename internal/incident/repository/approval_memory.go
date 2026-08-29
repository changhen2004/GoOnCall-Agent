package repository

import (
	"context"
	"sort"
	"sync"

	"gooncall-agent/internal/incident/model"
)

// MemoryApproval 是内存 ApprovalRepository。
type MemoryApproval struct {
	mu    sync.RWMutex
	items map[string]*model.Approval
}

// NewMemoryApproval 创建内存审批仓库。
func NewMemoryApproval() *MemoryApproval {
	return &MemoryApproval{items: make(map[string]*model.Approval)}
}

func (m *MemoryApproval) Create(_ context.Context, a *model.Approval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[a.ID]; ok {
		return ErrConflict
	}
	cp := *a
	m.items[a.ID] = &cp
	return nil
}

func (m *MemoryApproval) Update(_ context.Context, a *model.Approval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[a.ID]; !ok {
		return ErrNotFound
	}
	cp := *a
	m.items[a.ID] = &cp
	return nil
}

func (m *MemoryApproval) Get(_ context.Context, id string) (*model.Approval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	a, ok := m.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (m *MemoryApproval) ListByRun(_ context.Context, runID string) ([]*model.Approval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.Approval, 0)
	for _, a := range m.items {
		if a.RunID == runID {
			cp := *a
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryApproval) ListPending(_ context.Context) ([]*model.Approval, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.Approval, 0)
	for _, a := range m.items {
		if a.Status == model.ApprovalPending {
			cp := *a
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
