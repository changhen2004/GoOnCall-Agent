package repository

import (
	"context"

	"gooncall-agent/internal/incident/model"
)

// CreateWithEvent 在同一临界区内写入 Incident 与审计事件（等价于 Postgres 的同事务写入）。
//
// 先校验全部冲突条件再落盘，因此不存在"Incident 已写、事件没写"的中间状态；
// ev 为 nil 时等价于 Create。
func (m *Memory) CreateWithEvent(_ context.Context, inc *model.Incident, ev *model.IncidentEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkCreateConflicts(inc); err != nil {
		return err
	}
	if err := m.checkEventConflict(ev); err != nil {
		return err
	}
	if err := m.createLocked(inc); err != nil {
		return err
	}
	m.addEventLocked(ev)
	return nil
}

// UpdateWithEvent 在同一临界区内更新 Incident 与写入审计事件（version CAS 同时生效）。
// ev 为 nil 时等价于 Update。
func (m *Memory) UpdateWithEvent(_ context.Context, inc *model.Incident, ev *model.IncidentEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkUpdateConflicts(inc); err != nil {
		return err
	}
	if err := m.checkEventConflict(ev); err != nil {
		return err
	}
	if err := m.updateLocked(inc); err != nil {
		return err
	}
	inc.Version++
	m.addEventLocked(ev)
	return nil
}

// UpdateWithEvents 在同一临界区内更新 Incident 并写入多条事件。
func (m *Memory) UpdateWithEvents(_ context.Context, inc *model.Incident, events []*model.IncidentEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkUpdateConflicts(inc); err != nil {
		return err
	}
	seen := make(map[string]struct{}, len(events))
	for _, ev := range events {
		if err := m.checkEventConflict(ev); err != nil {
			return err
		}
		if ev != nil {
			if _, exists := seen[ev.ID]; exists {
				return ErrConflict
			}
			seen[ev.ID] = struct{}{}
		}
	}
	if err := m.updateLocked(inc); err != nil {
		return err
	}
	inc.Version++
	for _, ev := range events {
		m.addEventLocked(ev)
	}
	return nil
}

// ListEvents 返回某个 Incident 的事件（写入顺序即时间正序）。
func (m *Memory) ListEvents(_ context.Context, incidentID string) ([]*model.IncidentEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stored := m.events[incidentID]
	out := make([]*model.IncidentEvent, 0, len(stored))
	for _, ev := range stored {
		out = append(out, cloneEvent(ev))
	}
	return out, nil
}

// checkCreateConflicts / checkUpdateConflicts 只做校验，不产生写入。
func (m *Memory) checkCreateConflicts(inc *model.Incident) error {
	if _, ok := m.items[inc.ID]; ok {
		return ErrConflict
	}
	for _, existing := range m.items {
		if existing.Fingerprint != "" && existing.Fingerprint == inc.Fingerprint && !existing.Status.IsTerminal() {
			return ErrConflict
		}
	}
	return nil
}

func (m *Memory) checkUpdateConflicts(inc *model.Incident) error {
	cur, ok := m.items[inc.ID]
	if !ok {
		return ErrNotFound
	}
	if cur.Version != inc.Version {
		return ErrConcurrentModification
	}
	return nil
}

// checkEventConflict 校验事件主键是否重复（ev 为 nil 时通过）。
// 主键与 Postgres 一样是全局唯一的，因此跨 Incident 也要检测。
func (m *Memory) checkEventConflict(ev *model.IncidentEvent) error {
	if ev == nil {
		return nil
	}
	for _, list := range m.events {
		for _, existing := range list {
			if existing.ID == ev.ID {
				return ErrConflict
			}
		}
	}
	return nil
}

func (m *Memory) addEventLocked(ev *model.IncidentEvent) {
	if ev == nil {
		return
	}
	m.events[ev.IncidentID] = append(m.events[ev.IncidentID], cloneEvent(ev))
}

// cloneEvent 深拷贝事件（含 Payload map）：审计记录不能被调用方通过共享 map 改写。
func cloneEvent(ev *model.IncidentEvent) *model.IncidentEvent {
	if ev == nil {
		return nil
	}
	cp := *ev
	if ev.Payload != nil {
		payload := make(map[string]any, len(ev.Payload))
		for k, v := range ev.Payload {
			payload[k] = v
		}
		cp.Payload = payload
	}
	return &cp
}
