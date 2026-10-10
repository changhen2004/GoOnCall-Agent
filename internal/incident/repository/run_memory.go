package repository

import (
	"context"
	"sort"
	"sync"

	"gooncall-agent/internal/incident/model"
)

// MemoryRun 是内存 RunRepository，用于单元测试与本地开发。
type MemoryRun struct {
	mu    sync.RWMutex
	runs  map[string]*model.AgentRun
	steps map[string][]*model.AgentStep
	calls map[string][]*model.ToolCall
	evids map[string][]*model.Evidence
}

// NewMemoryRun 创建内存 RunRepository。
func NewMemoryRun() *MemoryRun {
	return &MemoryRun{
		runs:  make(map[string]*model.AgentRun),
		steps: make(map[string][]*model.AgentStep),
		calls: make(map[string][]*model.ToolCall),
		evids: make(map[string][]*model.Evidence),
	}
}

// CreateRun 原子创建 Run：ID 重复或同一 Incident 已存在 RUNNING/COMPLETED Run 时返回 ErrConflict
// （与 Postgres 部分唯一索引 idx_agent_run_active 语义一致）。
func (m *MemoryRun) CreateRun(_ context.Context, run *model.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; ok {
		return ErrConflict
	}
	for _, existing := range m.runs {
		if existing.IncidentID == run.IncidentID && existing.Status.IsActive() {
			return ErrConflict
		}
	}
	cp := *run
	m.runs[run.ID] = &cp
	return nil
}

// UpdateRun 更新 Run；把状态改成 RUNNING/COMPLETED 时若同一 Incident 已有其他活动 Run
// 则返回 ErrConflict（与 Postgres 部分唯一索引 idx_agent_run_active 行为一致）。
func (m *MemoryRun) UpdateRun(_ context.Context, run *model.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; !ok {
		return ErrNotFound
	}
	if run.Status.IsActive() {
		for _, existing := range m.runs {
			if existing.ID != run.ID && existing.IncidentID == run.IncidentID && existing.Status.IsActive() {
				return ErrConflict
			}
		}
	}
	cp := *run
	m.runs[run.ID] = &cp
	return nil
}

func (m *MemoryRun) GetRun(_ context.Context, id string) (*model.AgentRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	run, ok := m.runs[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *run
	return &cp, nil
}

// ListRunsByIncident 按 StartedAt 倒序；时间相同用 ID 正序兜底，保证顺序稳定。
func (m *MemoryRun) ListRunsByIncident(_ context.Context, incidentID string) ([]*model.AgentRun, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.AgentRun, 0)
	for _, run := range m.runs {
		if run.IncidentID == incidentID {
			cp := *run
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryRun) AddStep(_ context.Context, step *model.AgentStep) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *step
	m.steps[step.RunID] = append(m.steps[step.RunID], &cp)
	return nil
}

func (m *MemoryRun) ListSteps(_ context.Context, runID string) ([]*model.AgentStep, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.AgentStep, 0, len(m.steps[runID]))
	for _, s := range m.steps[runID] {
		cp := *s
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StepIndex < out[j].StepIndex })
	return out, nil
}

func (m *MemoryRun) AddToolCall(_ context.Context, call *model.ToolCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *call
	m.calls[call.RunID] = append(m.calls[call.RunID], &cp)
	return nil
}

// UpdateToolCall 补写工具结果（result / status / duration_ms），保留调用前的元数据。
func (m *MemoryRun) UpdateToolCall(_ context.Context, call *model.ToolCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.calls[call.RunID] {
		if existing.ID != call.ID {
			continue
		}
		existing.Result = call.Result
		existing.Status = call.Status
		existing.DurationMs = call.DurationMs
		return nil
	}
	return ErrNotFound
}

// ListToolCalls 按 CreatedAt 正序；时间相同用 ID 正序兜底，保证顺序稳定。
func (m *MemoryRun) ListToolCalls(_ context.Context, runID string) ([]*model.ToolCall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.ToolCall, 0, len(m.calls[runID]))
	for _, c := range m.calls[runID] {
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryRun) AddEvidence(_ context.Context, ev *model.Evidence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *ev
	m.evids[ev.RunID] = append(m.evids[ev.RunID], &cp)
	return nil
}

func (m *MemoryRun) ListEvidences(_ context.Context, runID string) ([]*model.Evidence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.Evidence, 0, len(m.evids[runID]))
	for _, e := range m.evids[runID] {
		cp := *e
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
