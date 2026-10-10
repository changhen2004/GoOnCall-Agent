package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/storage/postgres"
)

// 这些用例验证 Memory 与 Postgres 的契约一致（部分唯一索引、CAS、事件顺序、旧库升级）。
// 需要真实 PostgreSQL：设置 GOONCALL_TEST_POSTGRES_DSN 后运行（建议指向一次性数据库），未设置则跳过。
//
//	GOONCALL_TEST_POSTGRES_DSN='postgres://gooncall:gooncall@127.0.0.1:5433/gooncall_ab_test?sslmode=disable' \
//	 go test ./internal/incident/repository/... -run Postgres -count=1
func postgresContractDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("GOONCALL_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GOONCALL_TEST_POSTGRES_DSN not set; skipping PostgreSQL contract tests")
	}
	db, err := postgres.Connect(dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	return db
}

func applyMigration(t *testing.T, db *gorm.DB, name string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	if err := db.Exec(string(raw)).Error; err != nil {
		t.Fatalf("apply migration %s: %v", name, err)
	}
}

// contractRepos 聚合契约测试用的仓储，并保证所有表都存在（清理语句不会碰到缺失表）。
// Incident 与审计事件同属 incidents 仓储（CreateWithEvent/UpdateWithEvent/ListEvents）。
type contractRepos struct {
	db        *gorm.DB
	incidents *Postgres
	runs      *PostgresRun
	approvals *PostgresApproval
}

func newContractRepos(t *testing.T) *contractRepos {
	t.Helper()
	db := postgresContractDB(t)
	repos := &contractRepos{
		db: db, incidents: NewPostgres(db), runs: NewPostgresRun(db),
		approvals: NewPostgresApproval(db),
	}
	for _, name := range []string{
		"000001_init.sql",
		"000002_incident_version.sql",
		"000003_incident_fingerprint_unique.sql",
		"000004_active_incident_and_events.sql",
		"000005_approval_execution.sql",
	} {
		applyMigration(t, db, name)
	}
	return repos
}

// cleanupIncident 删除测试写入的行，保持共享测试库干净。
func cleanupIncident(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	t.Cleanup(func() {
		db.Exec("DELETE FROM agent_runs WHERE incident_id = ?", id)
		db.Exec("DELETE FROM approvals WHERE incident_id = ?", id)
		db.Exec("DELETE FROM incident_events WHERE incident_id = ?", id)
		db.Exec("DELETE FROM incidents WHERE id = ?", id)
	})
}

func testIncident(id, fingerprint string, status model.Status) *model.Incident {
	now := time.Now()
	return &model.Incident{
		ID: id, Service: "svc", Severity: "MEDIUM", Title: "t",
		Fingerprint: fingerprint, Status: status, StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}
}

func TestPostgres_ActiveFingerprintUniqueThenNewCycle(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)
	db, repo := r.db, r.incidents

	fp := fmt.Sprintf("fp-contract-%d", time.Now().UnixNano())
	first := testIncident("inc_contract_1_"+fp, fp, model.StatusOpen)
	cleanupIncident(t, db, first.ID)

	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("Create(first) error = %v", err)
	}
	// 同一指纹的第二个活动 Incident：部分唯一索引冲突。
	dup := testIncident("inc_contract_2_"+fp, fp, model.StatusInvestigating)
	cleanupIncident(t, db, dup.ID)
	if err := repo.Create(ctx, dup); !errors.Is(err, ErrConflict) {
		t.Fatalf("Create(dup active) error = %v, want ErrConflict", err)
	}

	// 终态化后同指纹可以开新周期。
	first.Status = model.StatusResolved
	if err := repo.Update(ctx, first); err != nil {
		t.Fatalf("Update(resolve) error = %v", err)
	}
	if err := repo.Create(ctx, dup); err != nil {
		t.Fatalf("Create(after terminal) error = %v, want nil", err)
	}

	active, err := repo.GetActiveByFingerprint(ctx, fp)
	if err != nil {
		t.Fatalf("GetActiveByFingerprint() error = %v", err)
	}
	if active.ID != dup.ID {
		t.Fatalf("active = %s, want %s (终态历史不算命中)", active.ID, dup.ID)
	}
	if _, err := repo.GetActiveByFingerprint(ctx, "fp-contract-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetActiveByFingerprint(missing) error = %v, want ErrNotFound", err)
	}
}

func TestPostgres_RunAtomicCreation(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)
	repo, runs := r.incidents, r.runs

	fp := fmt.Sprintf("fp-run-%d", time.Now().UnixNano())
	inc := testIncident("inc_contract_run_"+fp, fp, model.StatusInvestigating)
	cleanupIncident(t, r.db, inc.ID)
	if err := repo.Create(ctx, inc); err != nil {
		t.Fatalf("Create(incident) error = %v", err)
	}

	const workers = 5
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- runs.CreateRun(ctx, &model.AgentRun{
				ID: fmt.Sprintf("run_contract_%d_%s", i, inc.ID), IncidentID: inc.ID,
				AgentType: "diagnosis", Status: model.RunRunning, StartedAt: time.Now(),
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

	stored, err := runs.ListRunsByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListRunsByIncident() error = %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("runs = %d, want 1", len(stored))
	}

	// FAILED 后允许重试创建新 Run。
	failed := *stored[0]
	failed.Status = model.RunFailed
	if err := runs.UpdateRun(ctx, &failed); err != nil {
		t.Fatalf("UpdateRun() error = %v", err)
	}
	if err := runs.CreateRun(ctx, &model.AgentRun{
		ID: "run_contract_retry_" + inc.ID, IncidentID: inc.ID, Status: model.RunRunning, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateRun(after FAILED) error = %v, want nil", err)
	}

	// 反向更新：把已 FAILED 的旧 Run 改回 COMPLETED 会与活动 Run 冲突（Memory 同语义）。
	failed.Status = model.RunCompleted
	if err := runs.UpdateRun(ctx, &failed); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateRun(stale -> COMPLETED) error = %v, want ErrConflict", err)
	}

	// 更新不存在的 Run 必须 ErrNotFound，且不能把记录创建出来
	// （GORM Save 在 UPDATE 命中 0 行时会退化成 upsert，这里锁住显式 UPDATE 的行为）。
	ghost := &model.AgentRun{
		ID: "run_contract_ghost_" + inc.ID, IncidentID: inc.ID, AgentType: "diagnosis",
		Status: model.RunRunning, StartedAt: time.Now(),
	}
	if err := runs.UpdateRun(ctx, ghost); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateRun(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := runs.GetRun(ctx, ghost.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRun(ghost) error = %v, want ErrNotFound（UPDATE 不得创建记录）", err)
	}
	var ghostRows int64
	if err := r.db.Raw("SELECT count(*) FROM agent_runs WHERE id = ?", ghost.ID).Scan(&ghostRows).Error; err != nil {
		t.Fatalf("count ghost run: %v", err)
	}
	if ghostRows != 0 {
		t.Fatalf("ghost run rows = %d, want 0", ghostRows)
	}
}

func TestPostgres_ApprovalCASAndExpire(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)
	approvals := r.approvals

	fp := fmt.Sprintf("fp-approval-%d", time.Now().UnixNano())
	inc := testIncident("inc_contract_ap_"+fp, fp, model.StatusWaitingApproval)
	cleanupIncident(t, r.db, inc.ID)
	if err := r.incidents.Create(ctx, inc); err != nil {
		t.Fatalf("Create(incident) error = %v", err)
	}

	a := &model.Approval{
		ID: "ap_contract_" + fp, IncidentID: inc.ID, RunID: "run_contract", ToolCallID: "call_1",
		Action: "restart_worker", Status: model.ApprovalPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := approvals.Create(ctx, a); err != nil {
		t.Fatalf("Create(approval) error = %v", err)
	}

	// CAS：只有第一个 PENDING -> APPROVED 成功。
	approvedAt := time.Now()
	if _, err := approvals.TransitionStatus(ctx, a.ID, model.ApprovalPending, model.ApprovalApproved, func(a *model.Approval) {
		a.ApprovedBy = "admin"
		a.ApprovedAt = &approvedAt
	}); err != nil {
		t.Fatalf("TransitionStatus(claim) error = %v", err)
	}
	if _, err := approvals.TransitionStatus(ctx, a.ID, model.ApprovalPending, model.ApprovalApproved, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("TransitionStatus(second claim) error = %v, want ErrConflict", err)
	}
	if _, err := approvals.TransitionStatus(ctx, "ap_missing", model.ApprovalPending, model.ApprovalApproved, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TransitionStatus(missing) error = %v, want ErrNotFound", err)
	}

	// 执行与结果落库（含 operation_id）。
	if _, err := approvals.TransitionStatus(ctx, a.ID, model.ApprovalApproved, model.ApprovalExecuting, nil); err != nil {
		t.Fatalf("TransitionStatus(executing) error = %v", err)
	}
	if _, err := approvals.TransitionStatus(ctx, a.ID, model.ApprovalExecuting, model.ApprovalExecuted, func(a *model.Approval) {
		a.Result = `{"status":"completed"}`
		a.OperationID = "op-contract"
	}); err != nil {
		t.Fatalf("TransitionStatus(executed) error = %v", err)
	}
	reloaded, err := approvals.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if reloaded.OperationID != "op-contract" || reloaded.Status != model.ApprovalExecuted || reloaded.Result == "" {
		t.Fatalf("reloaded = %+v", reloaded)
	}
	if reloaded.ApprovedBy != "admin" || reloaded.ApprovedAt == nil {
		t.Fatalf("reloaded approved_by/at = %q/%v", reloaded.ApprovedBy, reloaded.ApprovedAt)
	}

	// 外部恢复：按 Incident 让 PENDING 审批过期。
	pending := &model.Approval{
		ID: "ap_contract_pending_" + fp, IncidentID: inc.ID, RunID: "run_contract", ToolCallID: "call_2",
		Action: "restart_worker", Status: model.ApprovalPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := approvals.Create(ctx, pending); err != nil {
		t.Fatalf("Create(pending) error = %v", err)
	}
	expired, err := approvals.ExpirePendingByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ExpirePendingByIncident() error = %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	reloaded, _ = approvals.Get(ctx, pending.ID)
	if reloaded.Status != model.ApprovalExpired {
		t.Fatalf("pending after expire = %s, want EXPIRED", reloaded.Status)
	}
}

func TestPostgres_EventOrderAndIsolation(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)

	fp := fmt.Sprintf("fp-event-%d", time.Now().UnixNano())
	inc := testIncident("inc_contract_evt_"+fp, fp, model.StatusOpen)
	cleanupIncident(t, r.db, inc.ID)

	created := model.NewIncidentEvent(inc.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	if err := r.incidents.CreateWithEvent(ctx, inc, created); err != nil {
		t.Fatalf("CreateWithEvent() error = %v", err)
	}
	otherInc := testIncident("inc_contract_other_"+fp, "fp-other-"+fp, model.StatusOpen)
	cleanupIncident(t, r.db, otherInc.ID)
	if err := r.incidents.CreateWithEvent(ctx, otherInc, model.NewIncidentEvent(otherInc.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")); err != nil {
		t.Fatalf("CreateWithEvent(other) error = %v", err)
	}

	transition := model.NewIncidentEvent(inc.ID, model.EventStatusTransition, model.StatusOpen, model.StatusInvestigating, "state machine transition", "")
	stored, err := r.incidents.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	stored.Status = model.StatusInvestigating
	if err := r.incidents.UpdateWithEvent(ctx, stored, transition); err != nil {
		t.Fatalf("UpdateWithEvent() error = %v", err)
	}

	got, err := r.incidents.ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("events = %d, want 2 (按 incident 隔离)", len(got))
	}
	if got[0].ID != created.ID || got[1].ID != transition.ID {
		t.Fatalf("order = %s, %s; want %s, %s", got[0].ID, got[1].ID, created.ID, transition.ID)
	}
	from, to := got[1].StatusChange()
	if from != model.StatusOpen || to != model.StatusInvestigating {
		t.Fatalf("StatusChange() = %s -> %s (jsonb 往返丢失?)", from, to)
	}
	if got[1].ReasonText() != "state machine transition" {
		t.Fatalf("ReasonText() = %q", got[1].ReasonText())
	}
}

// TestPostgres_EventWriteFailureRollsBackIncident 覆盖"Incident 与审计事件同事务"：
// 事件写入冲突（主键重复）时 Incident 必须整体回滚，create 与 update 两条路径都验证。
func TestPostgres_EventWriteFailureRollsBackIncident(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)

	fp := fmt.Sprintf("fp-atomic-%d", time.Now().UnixNano())
	inc := testIncident("inc_contract_atomic_"+fp, fp, model.StatusOpen)
	cleanupIncident(t, r.db, inc.ID)
	if err := r.incidents.Create(ctx, inc); err != nil {
		t.Fatalf("Create(incident) error = %v", err)
	}

	// 占用一个事件主键，随后用它制造事件写入失败。
	taken := model.NewIncidentEvent(inc.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	if err := r.db.Create(taken).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// create 路径：新 Incident 不应落库。
	fresh := testIncident("inc_contract_atomic2_"+fp, "fp-atomic2-"+fp, model.StatusOpen)
	cleanupIncident(t, r.db, fresh.ID)
	dup := model.NewIncidentEvent(fresh.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	dup.ID = taken.ID
	if err := r.incidents.CreateWithEvent(ctx, fresh, dup); err == nil {
		t.Fatal("CreateWithEvent(dup event) error = nil, want error")
	} else if errors.Is(err, ErrConflict) {
		t.Fatalf("CreateWithEvent(dup event) error = %v, want 事件错误而非 ErrConflict", err)
	}
	if _, err := r.incidents.GetByID(ctx, fresh.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("incident after failed event write = %v, want ErrNotFound (事务必须回滚)", err)
	}

	// update 路径：状态与 version 都不应变化。
	stored, err := r.incidents.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	before := stored.Version
	stored.Status = model.StatusInvestigating
	ev := model.NewIncidentEvent(inc.ID, model.EventStatusTransition, model.StatusOpen, model.StatusInvestigating, "state machine transition", "")
	ev.ID = taken.ID
	if err := r.incidents.UpdateWithEvent(ctx, stored, ev); err == nil {
		t.Fatal("UpdateWithEvent(dup event) error = nil, want error")
	}
	after, err := r.incidents.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if after.Status != model.StatusOpen || after.Version != before {
		t.Fatalf("incident after failed event write = %s v%d, want OPEN v%d", after.Status, after.Version, before)
	}
	if events, _ := r.incidents.ListEvents(ctx, inc.ID); len(events) != 1 {
		t.Fatalf("events = %d, want 1 (仅 seed 事件)", len(events))
	}
}

// TestPostgres_BackfillsLegacyApprovalIncidentID 覆盖旧库审批回填：
// 000005 之前创建的审批没有 incident_id，迁移必须通过 run_id 回填，
// 否则外部恢复时 ExpirePendingByIncident 匹配不到，旧审批仍能执行旧处置。
func TestPostgres_BackfillsLegacyApprovalIncidentID(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)

	fp := fmt.Sprintf("fp-backfill-%d", time.Now().UnixNano())
	inc := testIncident("inc_contract_backfill_"+fp, fp, model.StatusWaitingApproval)
	cleanupIncident(t, r.db, inc.ID)
	if err := r.incidents.Create(ctx, inc); err != nil {
		t.Fatalf("Create(incident) error = %v", err)
	}
	runID := "run_contract_backfill_" + fp
	if err := r.runs.CreateRun(ctx, &model.AgentRun{
		ID: runID, IncidentID: inc.ID, AgentType: "diagnosis", Status: model.RunCompleted, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	// 模拟 000005 之前的旧行：incident_id 为空。
	legacyID := "ap_legacy_" + fp
	if err := r.db.Exec(`INSERT INTO approvals (id, run_id, tool_call_id, action, reason, status, created_at)
VALUES (?, ?, ?, 'restart_worker', 'legacy', 'PENDING', now())`, legacyID, runID, "call_legacy").Error; err != nil {
		t.Fatalf("insert legacy approval: %v", err)
	}

	// 再执行 000005：应回填 incident_id。
	applyMigration(t, r.db, "000005_approval_execution.sql")
	got, err := r.approvals.Get(ctx, legacyID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.IncidentID != inc.ID {
		t.Fatalf("legacy approval incident_id = %q, want %q", got.IncidentID, inc.ID)
	}

	// 回填后外部恢复才能过期它。
	expired, err := r.approvals.ExpirePendingByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ExpirePendingByIncident() error = %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1 (回填前匹配不到旧审批)", expired)
	}
}

// TestPostgres_MigrationUpgradesLegacySchema 覆盖旧库升级路径：
//   - 旧的全局唯一索引 idx_incident_fingerprint 必须被清理（否则终态复发无法建新单）；
//   - 同一 Incident 残留的多条 RUNNING/COMPLETED Run 必须先归一化，否则部分唯一索引建不出来；
//   - 归一化不删除记录，只把较旧的置为 INTERRUPTED（保留最新一条）。
func TestPostgres_MigrationUpgradesLegacySchema(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)
	db, incidentRepo := r.db, r.incidents

	// 造出升级前状态：回退新的部分唯一索引，恢复同名的旧索引。
	for _, stmt := range []string{
		"DROP INDEX IF EXISTS idx_incident_active_fingerprint",
		"DROP INDEX IF EXISTS idx_agent_run_active",
		"CREATE INDEX IF NOT EXISTS idx_incident_fingerprint ON incidents (fingerprint)",
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("prepare legacy schema (%s): %v", stmt, err)
		}
	}

	fp := fmt.Sprintf("fp-legacy-%d", time.Now().UnixNano())
	inc := testIncident("inc_contract_legacy_"+fp, fp, model.StatusOpen)
	cleanupIncident(t, db, inc.ID)
	if err := incidentRepo.Create(ctx, inc); err != nil {
		t.Fatalf("Create(incident) error = %v", err)
	}

	base := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		if err := db.Exec(`INSERT INTO agent_runs (id, incident_id, agent_type, status, started_at) VALUES (?, ?, ?, ?, ?)`,
			fmt.Sprintf("run_legacy_%d_%s", i, inc.ID), inc.ID, "diagnosis", string(model.RunCompleted),
			base.Add(time.Duration(i)*time.Minute)).Error; err != nil {
			t.Fatalf("insert legacy run: %v", err)
		}
	}

	// 升级：000004 必须先归一化重复活动 Run，再建部分唯一索引。
	applyMigration(t, db, "000004_active_incident_and_events.sql")

	// 旧索引已清理，新的部分唯一索引存在。
	countIndex := func(table, index string) int64 {
		t.Helper()
		var n int64
		if err := db.Raw("SELECT count(*) FROM pg_indexes WHERE tablename = ? AND indexname = ?", table, index).Scan(&n).Error; err != nil {
			t.Fatalf("query index %s: %v", index, err)
		}
		return n
	}
	if n := countIndex("incidents", "idx_incident_fingerprint"); n != 0 {
		t.Fatalf("legacy index idx_incident_fingerprint still exists")
	}
	if n := countIndex("incidents", "idx_incident_active_fingerprint"); n != 1 {
		t.Fatalf("idx_incident_active_fingerprint count = %d, want 1", n)
	}
	if n := countIndex("agent_runs", "idx_agent_run_active"); n != 1 {
		t.Fatalf("idx_agent_run_active count = %d, want 1", n)
	}

	// 三条历史 Run 都还在，只有最新一条仍是活动状态。
	var total, active int64
	if err := db.Raw("SELECT count(*) FROM agent_runs WHERE incident_id = ?", inc.ID).Scan(&total).Error; err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if err := db.Raw("SELECT count(*) FROM agent_runs WHERE incident_id = ? AND status <> 'FAILED' AND status <> 'INTERRUPTED'", inc.ID).Scan(&active).Error; err != nil {
		t.Fatalf("count active runs: %v", err)
	}
	if total != 3 || active != 1 {
		t.Fatalf("runs total/active = %d/%d, want 3/1", total, active)
	}
	var kept string
	if err := db.Raw("SELECT id FROM agent_runs WHERE incident_id = ? AND status <> 'FAILED' AND status <> 'INTERRUPTED'", inc.ID).Scan(&kept).Error; err != nil {
		t.Fatalf("query kept run: %v", err)
	}
	if want := fmt.Sprintf("run_legacy_2_%s", inc.ID); kept != want {
		t.Fatalf("kept run = %s, want newest %s", kept, want)
	}
}

// TestPostgres_ToolCallUpdateAndStableOrder 与 Memory 保持一致：
// ToolCall 可补写结果，且 created_at 并列时按 id 正序（查询 API 的"排序稳定"验收条件）。
func TestPostgres_ToolCallUpdateAndStableOrder(t *testing.T) {
	ctx := context.Background()
	r := newContractRepos(t)
	runs := r.runs

	runID := "run_contract_toolcall_" + fmt.Sprintf("%d", time.Now().UnixNano())
	same := time.Now()
	for _, id := range []string{"call_c", "call_a", "call_b"} {
		if err := runs.AddToolCall(ctx, &model.ToolCall{
			ID: runID + "_" + id, RunID: runID, ToolName: "worker_restart",
			RiskLevel: "MEDIUM", Arguments: `{"target":"w"}`, Status: "RUNNING", CreatedAt: same,
		}); err != nil {
			t.Fatalf("AddToolCall(%s) error = %v", id, err)
		}
	}
	t.Cleanup(func() { r.db.Exec("DELETE FROM tool_calls WHERE run_id = ?", runID) })

	calls, err := runs.ListToolCalls(ctx, runID)
	if err != nil {
		t.Fatalf("ListToolCalls() error = %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("tool calls = %d, want 3", len(calls))
	}
	for i, want := range []string{"call_a", "call_b", "call_c"} {
		if calls[i].ID != runID+"_"+want {
			t.Fatalf("call[%d] = %s, want %s (created_at 并列须按 id 正序)", i, calls[i].ID, runID+"_"+want)
		}
	}

	target := calls[0]
	if err := runs.UpdateToolCall(ctx, &model.ToolCall{
		ID: target.ID, RunID: runID, Result: "done", Status: "COMPLETED", DurationMs: 7,
	}); err != nil {
		t.Fatalf("UpdateToolCall() error = %v", err)
	}
	updated, err := runs.ListToolCalls(ctx, runID)
	if err != nil {
		t.Fatalf("ListToolCalls() after update error = %v", err)
	}
	got := updated[0]
	if got.Result != "done" || got.Status != "COMPLETED" || got.DurationMs != 7 {
		t.Fatalf("updated call = %+v", got)
	}
	if got.RiskLevel != "MEDIUM" || got.Arguments != `{"target":"w"}` {
		t.Fatalf("update must keep call metadata, got %+v", got)
	}

	if err := runs.UpdateToolCall(ctx, &model.ToolCall{ID: runID + "_missing", RunID: runID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateToolCall(missing) error = %v, want ErrNotFound", err)
	}
}
