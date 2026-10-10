package repository

import (
	"context"
	"sort"
	"sync"
	"time"

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

// lessByCreatedAtThenID 按创建时间正序，时间并列用 ID 正序兜底（与 Postgres 的
// ORDER BY created_at ASC, id ASC 一致），保证并发写入与重启后顺序稳定。
func lessByCreatedAtThenID(a, b *model.Approval) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
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
	sort.Slice(out, func(i, j int) bool { return lessByCreatedAtThenID(out[i], out[j]) })
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
	sort.Slice(out, func(i, j int) bool { return lessByCreatedAtThenID(out[i], out[j]) })
	return out, nil
}

// TransitionStatus 原子条件更新（与 Postgres 实现语义一致）：
// 状态不等于 from 时返回 ErrConflict，记录不存在返回 ErrNotFound。
func (m *MemoryApproval) TransitionStatus(_ context.Context, id string, from, to model.ApprovalStatus, mutate func(*model.Approval)) (*model.Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	if cur.Status != from {
		return nil, ErrConflict
	}
	next := *cur
	next.Status = to
	next.UpdatedAt = time.Now()
	if mutate != nil {
		mutate(&next)
	}
	m.items[id] = &next
	out := next
	return &out, nil
}

// ExpirePendingByIncident 把该 Incident 下所有 PENDING 审批置为 EXPIRED。
// 空 incidentID 不匹配任何审批（Service 层已拒绝空值）。
func (m *MemoryApproval) ExpirePendingByIncident(_ context.Context, incidentID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	expired := 0
	for id, a := range m.items {
		if incidentID == "" || a.IncidentID != incidentID || a.Status != model.ApprovalPending {
			continue
		}
		next := *a
		next.Status = model.ApprovalExpired
		next.Error = "incident resolved externally"
		next.UpdatedAt = time.Now()
		m.items[id] = &next
		expired++
	}
	return expired, nil
}
