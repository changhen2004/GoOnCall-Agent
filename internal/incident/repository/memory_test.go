package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gooncall-agent/internal/incident/model"
)

func newTestIncident(id, service, status string) *model.Incident {
	now := time.Now()
	return &model.Incident{
		ID:        id,
		Service:   service,
		Status:    model.Status(status),
		Title:     "test incident",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestMemory_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	inc := newTestIncident("inc_1", "svc-a", "OPEN")

	if err := repo.Create(ctx, inc); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.GetByID(ctx, "inc_1")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.ID != "inc_1" || got.Service != "svc-a" {
		t.Fatalf("GetByID() = %+v", got)
	}
}

func TestMemory_GetByIDNotFound(t *testing.T) {
	repo := NewMemory()
	if _, err := repo.GetByID(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID() error = %v, want ErrNotFound", err)
	}
}

func TestMemory_GetByFingerprint(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	inc := newTestIncident("inc_1", "svc-a", "OPEN")
	inc.Fingerprint = "fp_1"
	if err := repo.Create(ctx, inc); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.GetByFingerprint(ctx, "fp_1")
	if err != nil {
		t.Fatalf("GetByFingerprint() error = %v", err)
	}
	if got.ID != "inc_1" {
		t.Fatalf("GetByFingerprint() = %+v", got)
	}

	if _, err := repo.GetByFingerprint(ctx, "fp_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByFingerprint(missing) error = %v, want ErrNotFound", err)
	}
}

func TestMemory_CreateConflict(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	if err := repo.Create(ctx, newTestIncident("inc_1", "svc-a", "OPEN")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repo.Create(ctx, newTestIncident("inc_1", "svc-a", "OPEN")); !errors.Is(err, ErrConflict) {
		t.Fatalf("Create(dup) error = %v, want ErrConflict", err)
	}
}

func TestMemory_CreateFingerprintConflict(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()

	a := newTestIncident("inc_1", "svc-a", "OPEN")
	a.Fingerprint = "fp-1"
	b := newTestIncident("inc_2", "svc-a", "OPEN")
	b.Fingerprint = "fp-1"

	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// 不同 ID 但同 fingerprint：活动记录唯一冲突
	if err := repo.Create(ctx, b); !errors.Is(err, ErrConflict) {
		t.Fatalf("Create(same fingerprint) error = %v, want ErrConflict", err)
	}
}

// TestMemory_CreateFingerprintAllowedAfterTerminal 验证部分唯一语义：
// 指纹已终态时允许创建新 Incident（新一轮告警周期）。
func TestMemory_CreateFingerprintAllowedAfterTerminal(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()

	a := newTestIncident("inc_1", "svc-a", "RESOLVED")
	a.Fingerprint = "fp-1"
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create(resolved) error = %v", err)
	}

	b := newTestIncident("inc_2", "svc-a", "OPEN")
	b.Fingerprint = "fp-1"
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("Create(new cycle) error = %v, want nil", err)
	}

	active, err := repo.GetActiveByFingerprint(ctx, "fp-1")
	if err != nil {
		t.Fatalf("GetActiveByFingerprint() error = %v", err)
	}
	if active.ID != "inc_2" {
		t.Fatalf("active incident = %s, want inc_2 (终态历史不算命中)", active.ID)
	}

	// 新一轮也终态化后，活动查询回到不存在。
	b.Status = model.StatusResolved
	if err := repo.Update(ctx, b); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := repo.GetActiveByFingerprint(ctx, "fp-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetActiveByFingerprint(after terminal) error = %v, want ErrNotFound", err)
	}
	// 两条历史记录都保留（不删除）。
	all, err := repo.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("incidents = %d, want 2", len(all))
	}
}

func TestMemory_GetActiveByFingerprintNotFound(t *testing.T) {
	repo := NewMemory()
	if _, err := repo.GetActiveByFingerprint(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetActiveByFingerprint() error = %v, want ErrNotFound", err)
	}
}

// TestMemoryRun_CreateRunIsAtomic 覆盖 Run 原子创建：
// 并发创建同一 Incident 的 Run 只有一个成功；活动 Run 未终态前一律冲突；
// Run 落为 FAILED 后允许重试创建新 Run。
func TestMemoryRun_CreateRunIsAtomic(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRun()

	const workers = 10
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- repo.CreateRun(ctx, &model.AgentRun{
				ID:         fmt.Sprintf("run_%d", i),
				IncidentID: "inc_1",
				AgentType:  "diagnosis",
				Status:     model.RunRunning,
				StartedAt:  time.Now(),
			})
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	var okCount, conflictCount int
	for err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, ErrConflict):
			conflictCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || conflictCount != workers-1 {
		t.Fatalf("ok = %d, conflict = %d, want 1/%d", okCount, conflictCount, workers-1)
	}

	runs, err := repo.ListRunsByIncident(ctx, "inc_1")
	if err != nil {
		t.Fatalf("ListRunsByIncident() error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}

	// COMPLETED 与 RUNNING 同属"活动"，仍然冲突。
	completed := *runs[0]
	completed.Status = model.RunCompleted
	if err := repo.UpdateRun(ctx, &completed); err != nil {
		t.Fatalf("UpdateRun() error = %v", err)
	}
	if err := repo.CreateRun(ctx, &model.AgentRun{ID: "run_retry", IncidentID: "inc_1", Status: model.RunRunning, StartedAt: time.Now()}); !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateRun(while COMPLETED) error = %v, want ErrConflict", err)
	}

	// FAILED 后允许重试创建新 Run。
	completed.Status = model.RunFailed
	if err := repo.UpdateRun(ctx, &completed); err != nil {
		t.Fatalf("UpdateRun(failed) error = %v", err)
	}
	if err := repo.CreateRun(ctx, &model.AgentRun{ID: "run_retry", IncidentID: "inc_1", Status: model.RunRunning, StartedAt: time.Now()}); err != nil {
		t.Fatalf("CreateRun(after FAILED) error = %v, want nil", err)
	}
}

// TestMemoryRun_UpdateRunMissingIsNotFound 仓储一致性：更新不存在的 Run 必须返回
// ErrNotFound，绝不能把它创建出来（Postgres 侧曾因 GORM Save 的 upsert 行为违反这一点）。
func TestMemoryRun_UpdateRunMissingIsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRun()

	err := repo.UpdateRun(ctx, &model.AgentRun{ID: "run_missing", IncidentID: "inc_1", Status: model.RunRunning, StartedAt: time.Now()})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateRun(missing) error = %v, want ErrNotFound", err)
	}
	runs, err := repo.ListRunsByIncident(ctx, "inc_1")
	if err != nil {
		t.Fatalf("ListRunsByIncident() error = %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs = %d, want 0（更新不得创建记录）", len(runs))
	}
}

func TestMemory_ListFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	for i, id := range []string{"inc_1", "inc_2", "inc_3"} {
		inc := newTestIncident(id, "svc-a", "OPEN")
		inc.CreatedAt = inc.CreatedAt.Add(time.Duration(i) * time.Second)
		if err := repo.Create(ctx, inc); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	if err := repo.Create(ctx, newTestIncident("inc_4", "svc-b", "RESOLVED")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	all, err := repo.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List() len = %d, want 4", len(all))
	}

	svcA, _ := repo.List(ctx, ListFilter{Service: "svc-a"})
	if len(svcA) != 3 {
		t.Fatalf("List(service=svc-a) len = %d, want 3", len(svcA))
	}

	resolved, _ := repo.List(ctx, ListFilter{Status: model.StatusResolved})
	if len(resolved) != 1 || resolved[0].ID != "inc_4" {
		t.Fatalf("List(status=RESOLVED) = %+v", resolved)
	}

	limited, _ := repo.List(ctx, ListFilter{Limit: 2})
	if len(limited) != 2 {
		t.Fatalf("List(limit=2) len = %d, want 2", len(limited))
	}

	offset, _ := repo.List(ctx, ListFilter{Offset: 3})
	if len(offset) != 1 {
		t.Fatalf("List(offset=3) len = %d, want 1", len(offset))
	}
}

func TestMemory_Update(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	if err := repo.Create(ctx, newTestIncident("inc_1", "svc-a", "OPEN")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	inc, _ := repo.GetByID(ctx, "inc_1")
	inc.Status = model.StatusInvestigating
	if err := repo.Update(ctx, inc); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ := repo.GetByID(ctx, "inc_1")
	if got.Status != model.StatusInvestigating {
		t.Fatalf("status = %s, want INVESTIGATING", got.Status)
	}
}

func TestMemory_UpdateNotFound(t *testing.T) {
	repo := NewMemory()
	if err := repo.Update(context.Background(), newTestIncident("missing", "svc-a", "OPEN")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v, want ErrNotFound", err)
	}
}

func TestMemory_UpdateConcurrentModification(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	if err := repo.Create(ctx, newTestIncident("inc_1", "svc-a", "OPEN")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 两个"并发"快照读到同一版本
	a, _ := repo.GetByID(ctx, "inc_1")
	b, _ := repo.GetByID(ctx, "inc_1")
	if a.Version != 0 || b.Version != 0 {
		t.Fatalf("initial versions = %d/%d, want 0", a.Version, b.Version)
	}
	a.Status = model.StatusInvestigating
	b.Status = model.StatusMitigating

	// 第一个更新成功，version 0 -> 1
	if err := repo.Update(ctx, a); err != nil {
		t.Fatalf("first Update() error = %v", err)
	}
	if a.Version != 1 {
		t.Fatalf("version after first update = %d, want 1", a.Version)
	}

	// 第二个用旧版本更新 -> 并发冲突
	if err := repo.Update(ctx, b); !errors.Is(err, ErrConcurrentModification) {
		t.Fatalf("second Update() error = %v, want ErrConcurrentModification", err)
	}

	// 重新读取后版本一致，可再次更新
	cur, _ := repo.GetByID(ctx, "inc_1")
	if cur.Version != 1 {
		t.Fatalf("stored version = %d, want 1", cur.Version)
	}
	cur.Status = model.StatusVerifying
	if err := repo.Update(ctx, cur); err != nil {
		t.Fatalf("Update() after refresh error = %v", err)
	}
	if cur.Version != 2 {
		t.Fatalf("version after refresh update = %d, want 2", cur.Version)
	}
}

// TestMemoryOrdering_EqualTimestampsAreStable 时间戳并列时（同一毫秒批量写入）
// 必须用 ID 兜底排序，否则查询 API 的顺序在并发写入/重启后会漂移。
func TestMemoryOrdering_EqualTimestampsAreStable(t *testing.T) {
	ctx := context.Background()
	same := time.Now()

	runs := NewMemoryRun()
	// 故意乱序写入，期望按 ID 正序返回。
	for _, id := range []string{"run_c", "run_a", "run_b"} {
		if err := runs.CreateRun(ctx, &model.AgentRun{
			ID: id, IncidentID: "inc_1", AgentType: "diagnosis",
			Status: model.RunFailed, StartedAt: same,
		}); err != nil {
			t.Fatalf("CreateRun(%s) error = %v", id, err)
		}
	}
	got, err := runs.ListRunsByIncident(ctx, "inc_1")
	if err != nil {
		t.Fatalf("ListRunsByIncident() error = %v", err)
	}
	assertIDs(t, "runs", runIDs(got), []string{"run_a", "run_b", "run_c"})

	for _, id := range []string{"call_c", "call_a", "call_b"} {
		if err := runs.AddToolCall(ctx, &model.ToolCall{
			ID: id, RunID: "run_1", ToolName: "runbook_search",
			RiskLevel: "LOW", Status: "COMPLETED", CreatedAt: same,
		}); err != nil {
			t.Fatalf("AddToolCall(%s) error = %v", id, err)
		}
	}
	calls, err := runs.ListToolCalls(ctx, "run_1")
	if err != nil {
		t.Fatalf("ListToolCalls() error = %v", err)
	}
	if got := []string{calls[0].ID, calls[1].ID, calls[2].ID}; !equalIDs(got, []string{"call_a", "call_b", "call_c"}) {
		t.Fatalf("tool calls = %v, want [call_a call_b call_c]", got)
	}

	approvals := NewMemoryApproval()
	for _, id := range []string{"ap_c", "ap_a", "ap_b"} {
		if err := approvals.Create(ctx, &model.Approval{
			ID: id, RunID: "run_1", ToolCallID: "call_a", Action: "restart_worker",
			Status: model.ApprovalPending, CreatedAt: same,
		}); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	byRun, err := approvals.ListByRun(ctx, "run_1")
	if err != nil {
		t.Fatalf("ListByRun() error = %v", err)
	}
	assertIDs(t, "approvals by run", approvalIDs(byRun), []string{"ap_a", "ap_b", "ap_c"})

	pending, err := approvals.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending() error = %v", err)
	}
	assertIDs(t, "pending approvals", approvalIDs(pending), []string{"ap_a", "ap_b", "ap_c"})
}

// TestMemoryRun_UpdateToolCall 补写结果保留调用前的元数据；记录不存在返回 ErrNotFound。
func TestMemoryRun_UpdateToolCall(t *testing.T) {
	ctx := context.Background()
	repo := NewMemoryRun()
	if err := repo.AddToolCall(ctx, &model.ToolCall{
		ID: "call_1", RunID: "run_1", ToolName: "worker_restart",
		RiskLevel: "MEDIUM", Arguments: `{"target":"w"}`, Status: "RUNNING", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("AddToolCall() error = %v", err)
	}

	if err := repo.UpdateToolCall(ctx, &model.ToolCall{
		ID: "call_1", RunID: "run_1", Result: "done", Status: "COMPLETED", DurationMs: 12,
	}); err != nil {
		t.Fatalf("UpdateToolCall() error = %v", err)
	}
	calls, _ := repo.ListToolCalls(ctx, "run_1")
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	c := calls[0]
	if c.Result != "done" || c.Status != "COMPLETED" || c.DurationMs != 12 {
		t.Fatalf("updated call = %+v", c)
	}
	if c.RiskLevel != "MEDIUM" || c.Arguments != `{"target":"w"}` || c.ToolName != "worker_restart" {
		t.Fatalf("update must keep call metadata, got %+v", c)
	}

	if err := repo.UpdateToolCall(ctx, &model.ToolCall{ID: "missing", RunID: "run_1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateToolCall(unknown) error = %v, want ErrNotFound", err)
	}
}

func runIDs(runs []*model.AgentRun) []string {
	out := make([]string, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.ID)
	}
	return out
}

func approvalIDs(items []*model.Approval) []string {
	out := make([]string, 0, len(items))
	for _, a := range items {
		out = append(out, a.ID)
	}
	return out
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func assertIDs(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !equalIDs(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}
