package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"gooncall-agent/internal/incident/model"
)

// TestMemory_CreateWithEventIsAtomic 覆盖"Incident 与审计事件同事务"语义：
// 事件写入冲突时 Incident 必须整体不落库（不能出现状态已提交、事件缺失）。
func TestMemory_CreateWithEventIsAtomic(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()

	// 先占用一个事件 ID，制造事件主键冲突。
	taken := model.NewIncidentEvent("inc_taken", model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	if err := repo.CreateWithEvent(ctx, testIncident("inc_taken", "fp-taken", model.StatusOpen), taken); err != nil {
		t.Fatalf("seed CreateWithEvent() error = %v", err)
	}

	inc := testIncident("inc_atomic", "fp-atomic", model.StatusOpen)
	dup := model.NewIncidentEvent(inc.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	dup.ID = taken.ID // 与已存在事件同主键

	if err := repo.CreateWithEvent(ctx, inc, dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateWithEvent(dup event) error = %v, want ErrConflict", err)
	}
	if _, err := repo.GetByID(ctx, inc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("incident after failed event write = %v, want ErrNotFound (必须整体回滚)", err)
	}
	events, err := repo.ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0", len(events))
	}
}

// TestMemory_UpdateWithEventIsAtomic 覆盖更新路径：事件写入失败时状态与 version 都必须保持不变。
func TestMemory_UpdateWithEventIsAtomic(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()

	inc := testIncident("inc_atomic_update", "fp-atomic-update", model.StatusOpen)
	created := model.NewIncidentEvent(inc.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	if err := repo.CreateWithEvent(ctx, inc, created); err != nil {
		t.Fatalf("CreateWithEvent() error = %v", err)
	}

	cur, err := repo.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	cur.Status = model.StatusInvestigating
	cur.UpdatedAt = time.Now()
	// 与已落库的创建事件同主键：事件写入必然冲突。
	ev := model.NewIncidentEvent(cur.ID, model.EventStatusTransition, model.StatusOpen, model.StatusInvestigating, "state machine transition", "")
	ev.ID = created.ID
	if err := repo.UpdateWithEvent(ctx, cur, ev); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateWithEvent(dup event) error = %v, want ErrConflict", err)
	}

	stored, err := repo.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if stored.Status != model.StatusOpen || stored.Version != 0 {
		t.Fatalf("stored = %s v%d, want unchanged OPEN v0", stored.Status, stored.Version)
	}
	if events, _ := repo.ListEvents(ctx, inc.ID); len(events) != 1 {
		t.Fatalf("events = %d, want 1 (仅创建事件)", len(events))
	}
}

func TestMemory_EventsListAndIsolation(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()

	if events, err := repo.ListEvents(ctx, "inc_1"); err != nil || len(events) != 0 {
		t.Fatalf("ListEvents(empty) = %v, %v; want empty, nil", events, err)
	}

	created := model.NewIncidentEvent("inc_1", model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	if err := repo.CreateWithEvent(ctx, testIncident("inc_1", "fp-1", model.StatusOpen), created); err != nil {
		t.Fatalf("CreateWithEvent() error = %v", err)
	}
	transition := model.NewIncidentEvent("inc_1", model.EventStatusTransition, model.StatusOpen, model.StatusInvestigating, "state machine transition", "")
	if err := repo.CreateWithEvent(ctx, testIncident("inc_2", "fp-2", model.StatusOpen), model.NewIncidentEvent("inc_2", model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")); err != nil {
		t.Fatalf("CreateWithEvent(inc_2) error = %v", err)
	}
	inc1, _ := repo.GetByID(ctx, "inc_1")
	inc1.Status = model.StatusInvestigating
	if err := repo.UpdateWithEvent(ctx, inc1, transition); err != nil {
		t.Fatalf("UpdateWithEvent() error = %v", err)
	}

	events, err := repo.ListEvents(ctx, "inc_1")
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 (按 incident 隔离)", len(events))
	}
	// 写入顺序即时间正序
	if events[0].ID != created.ID || events[1].ID != transition.ID {
		t.Fatalf("event order = %s, %s; want %s, %s", events[0].ID, events[1].ID, created.ID, transition.ID)
	}
	from, to := events[1].StatusChange()
	if from != model.StatusOpen || to != model.StatusInvestigating {
		t.Fatalf("StatusChange() = %s -> %s", from, to)
	}
	if events[1].ReasonText() != "state machine transition" {
		t.Fatalf("ReasonText() = %q", events[1].ReasonText())
	}
	if events[1].CreatedAt.IsZero() {
		t.Fatal("CreatedAt should be set")
	}
}

// TestMemory_EventsDeepCopyPayload 覆盖审计数据不可被调用方篡改：
// 写入后修改入参、读取后修改返回值，都不能影响存储。
func TestMemory_EventsDeepCopyPayload(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()

	inc := testIncident("inc_copy", "fp-copy", model.StatusOpen)
	ev := model.NewIncidentEvent(inc.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	if err := repo.CreateWithEvent(ctx, inc, ev); err != nil {
		t.Fatalf("CreateWithEvent() error = %v", err)
	}

	// 修改入参不能影响存储。
	ev.Payload["reason"] = "tampered"
	ev.Payload["injected"] = true

	got, err := repo.ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if got[0].ReasonText() != "alert firing" {
		t.Fatalf("stored reason = %q, want alert firing", got[0].ReasonText())
	}
	if _, ok := got[0].Payload["injected"]; ok {
		t.Fatal("stored payload was mutated through the caller's map")
	}

	// 修改读取结果也不能影响存储。
	got[0].Payload["reason"] = "tampered again"
	got[0].Payload["injected"] = true
	again, err := repo.ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if again[0].ReasonText() != "alert firing" {
		t.Fatalf("stored reason after read mutation = %q, want alert firing", again[0].ReasonText())
	}
	if _, ok := again[0].Payload["injected"]; ok {
		t.Fatal("stored payload was mutated through the returned map")
	}
}

func TestIncidentEvent_ReasonFallsBackToError(t *testing.T) {
	ev := model.NewIncidentEvent("inc_1", model.EventStatusTransition, model.StatusOpen, model.StatusFailed, "", "llm timeout")
	if ev.ReasonText() != "llm timeout" {
		t.Fatalf("ReasonText() = %q, want llm timeout", ev.ReasonText())
	}
	if ev.Payload["error"] != "llm timeout" {
		t.Fatalf("payload error = %v", ev.Payload["error"])
	}
}
