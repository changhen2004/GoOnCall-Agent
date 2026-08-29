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

func (m *MemoryRun) CreateRun(_ context.Context, run *model.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; ok {
		return ErrConflict
	}
	cp := *run
	m.runs[run.ID] = &cp
	return nil
}

func (m *MemoryRun) UpdateRun(_ context.Context, run *model.AgentRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; !ok {
		return ErrNotFound
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
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
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

func (m *MemoryRun) ListToolCalls(_ context.Context, runID string) ([]*model.ToolCall, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*model.ToolCall, 0, len(m.calls[runID]))
	for _, c := range m.calls[runID] {
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
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
