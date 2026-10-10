package approval

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
)

type fakeExecutor struct {
	mu          sync.Mutex
	result      string
	operationID string
	err         error
	calls       int
	lastCtx     context.Context
	onExecute   func()
}

func (f *fakeExecutor) Execute(ctx context.Context, action, arguments, runID string) (ExecutionResult, error) {
	f.mu.Lock()
	f.calls++
	f.lastCtx = ctx
	hook := f.onExecute
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return ExecutionResult{Output: f.result, OperationID: f.operationID}, f.err
}

func (f *fakeExecutor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newService(executor ActionExecutor) (*Service, *repository.MemoryApproval, *runtime.StreamBroker) {
	repo := repository.NewMemoryApproval()
	broker := runtime.NewStreamBroker()
	return New(repo, broker, executor), repo, broker
}

// wiredService 装配完整的 Run/Incident 关联：事件、内存 Run 仓库与 Incident 服务。
type wiredService struct {
	svc        *Service
	approvals  *repository.MemoryApproval
	runs       *repository.MemoryRun
	incidents  *incidentservice.Service
	broker     *runtime.StreamBroker
	incidentID string
	runID      string
}

// newWiredService 建一个 INVESTIGATING 的 Incident 和它的 RUNNING Run。
func newWiredService(t *testing.T, executor ActionExecutor) *wiredService {
	t.Helper()
	ctx := context.Background()
	incidentRepo := repository.NewMemory()
	incidentSvc := incidentservice.New(incidentRepo)
	runs := repository.NewMemoryRun()
	broker := runtime.NewStreamBroker()
	approvals := repository.NewMemoryApproval()

	inc, _, err := incidentSvc.Create(ctx, incidentservice.CreateIncidentInput{Service: "svc", AlertName: "a", Title: "t"})
	if err != nil {
		t.Fatalf("create incident: %v", err)
	}
	if _, err := incidentSvc.Analyze(ctx, inc.ID); err != nil {
		t.Fatalf("analyze incident: %v", err)
	}
	runID := "run_1"
	if err := runs.CreateRun(ctx, &model.AgentRun{
		ID: runID, IncidentID: inc.ID, AgentType: "diagnosis", Status: model.RunRunning, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	svc := New(approvals, broker, executor).WithIncidentState(incidentSvc, runs)
	return &wiredService{svc: svc, approvals: approvals, runs: runs, incidents: incidentSvc, broker: broker, incidentID: inc.ID, runID: runID}
}

func TestRequest_ThenApprove_Executes(t *testing.T) {
	ex := &fakeExecutor{result: "worker restarted"}
	svc, _, broker := newService(ex)

	a, err := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", `{"target":"worker"}`, "consumer count dropped")
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if a.Status != model.ApprovalPending {
		t.Fatalf("status = %s, want PENDING", a.Status)
	}

	got, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if got.Status != model.ApprovalExecuted {
		t.Fatalf("status = %s, want EXECUTED", got.Status)
	}
	if got.ApprovedBy != "admin" {
		t.Fatalf("approved_by = %q, want admin", got.ApprovedBy)
	}
	if ex.callCount() != 1 {
		t.Fatalf("executor calls = %d, want 1", ex.callCount())
	}
	// 幂等键必须通过 context 传给执行器，且等于 approval_id。
	if key := ApprovalIDFrom(ex.lastCtx); key != a.ID {
		t.Fatalf("executor approval id = %q, want %q", key, a.ID)
	}

	// 事件
	ch, cancel := broker.Subscribe("run_1")
	defer cancel()
	seen := collectEvents(t, ch, []string{"approval.required", "action.approved", "action.executing", "action.completed"})
	_ = seen
}

func TestApprove_PersistsResultAndOperationID(t *testing.T) {
	ex := &fakeExecutor{result: `{"status":"completed","operation_id":"op-123"}`, operationID: "op-123"}
	svc, repo, _ := newService(ex)

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	got, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if got.OperationID != "op-123" {
		t.Fatalf("operation_id = %q, want op-123", got.OperationID)
	}
	if got.Result != ex.result {
		t.Fatalf("result = %q, want %q", got.Result, ex.result)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("updated_at should be set")
	}
	stored, _ := repo.Get(context.Background(), a.ID)
	if stored.Status != model.ApprovalExecuted || stored.OperationID != "op-123" {
		t.Fatalf("stored = %+v", stored)
	}
}

// recordingApprovalRepo 记录每次 CAS 落库的状态，验证审计状态链。
type recordingApprovalRepo struct {
	*repository.MemoryApproval
	mu       sync.Mutex
	statuses []model.ApprovalStatus
}

func (r *recordingApprovalRepo) TransitionStatus(ctx context.Context, id string, from, to model.ApprovalStatus, mutate func(*model.Approval)) (*model.Approval, error) {
	a, err := r.MemoryApproval.TransitionStatus(ctx, id, from, to, mutate)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.statuses = append(r.statuses, a.Status)
	r.mu.Unlock()
	return a, nil
}

func TestApprove_StateChain(t *testing.T) {
	repo := &recordingApprovalRepo{MemoryApproval: repository.NewMemoryApproval()}
	svc := New(repo, runtime.NewStreamBroker(), &fakeExecutor{result: "ok"})

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	if _, err := svc.Approve(context.Background(), a.ID, "admin"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	want := []model.ApprovalStatus{
		model.ApprovalApproved,
		model.ApprovalExecuting,
		model.ApprovalExecuted,
	}
	if len(repo.statuses) != len(want) {
		t.Fatalf("persisted states = %v, want %v", repo.statuses, want)
	}
	for i, s := range want {
		if repo.statuses[i] != s {
			t.Fatalf("persisted states = %v, want %v", repo.statuses, want)
		}
	}
}

func TestApprove_ExecutorFails(t *testing.T) {
	ex := &fakeExecutor{err: errors.New("restart failed: connection refused")}
	svc, repo, _ := newService(ex)

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	got, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if got.Status != model.ApprovalFailed {
		t.Fatalf("status = %s, want FAILED", got.Status)
	}
	if got.Error != "restart failed: connection refused" {
		t.Fatalf("error = %q", got.Error)
	}
	if got.Result != "" {
		t.Fatalf("result = %q, want empty on failure", got.Result)
	}
	if ex.callCount() != 1 {
		t.Fatalf("executor calls = %d, want 1", ex.callCount())
	}
	stored, _ := repo.Get(context.Background(), a.ID)
	if stored.Status != model.ApprovalFailed || stored.Error == "" {
		t.Fatalf("stored = %+v", stored)
	}
}

// TestApprove_ResultUnknown 覆盖超时语义：结果未知必须落为 UNKNOWN，而不是 FAILED/EXECUTED。
func TestApprove_ResultUnknown(t *testing.T) {
	ex := &fakeExecutor{err: fmt.Errorf("post restart: context deadline exceeded: %w", ErrResultUnknown)}
	svc, repo, _ := newService(ex)

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	got, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if got.Status != model.ApprovalUnknown {
		t.Fatalf("status = %s, want UNKNOWN", got.Status)
	}
	stored, _ := repo.Get(context.Background(), a.ID)
	if stored.Status != model.ApprovalUnknown {
		t.Fatalf("stored status = %s, want UNKNOWN", stored.Status)
	}
	// UNKNOWN 是终态：再次批准返回 conflict，且不重复执行。
	if _, err := svc.Approve(context.Background(), a.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second approve error = %v, want ErrNotPending", err)
	}
	if ex.callCount() != 1 {
		t.Fatalf("executor calls = %d, want 1", ex.callCount())
	}
}

func TestApprove_NoExecutor(t *testing.T) {
	svc, _, _ := newService(nil)

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	got, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	// 无执行器：无实际执行，状态停留在 APPROVED
	if got.Status != model.ApprovalApproved {
		t.Fatalf("status = %s, want APPROVED", got.Status)
	}
}

func TestReject(t *testing.T) {
	ex := &fakeExecutor{}
	svc, _, _ := newService(ex)

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "reason")
	got, err := svc.Reject(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if got.Status != model.ApprovalRejected {
		t.Fatalf("status = %s, want REJECTED", got.Status)
	}
	if ex.callCount() != 0 {
		t.Fatal("executor should NOT be called on reject")
	}
}

func TestApprove_NotPending(t *testing.T) {
	svc, _, _ := newService(&fakeExecutor{})
	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	_, _ = svc.Approve(context.Background(), a.ID, "admin")

	if _, err := svc.Approve(context.Background(), a.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second approve error = %v, want ErrNotPending", err)
	}
}

func TestApprove_RejectedAndExpiredReturnConflict(t *testing.T) {
	svc, repo, _ := newService(&fakeExecutor{})

	rejected, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	if _, err := svc.Reject(context.Background(), rejected.ID, "admin"); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if _, err := svc.Approve(context.Background(), rejected.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approve rejected error = %v, want ErrNotPending", err)
	}

	expired, _ := svc.Request(context.Background(), "run_1", "call_2", "restart_worker", "{}", "r")
	if _, err := repo.TransitionStatus(context.Background(), expired.ID, model.ApprovalPending, model.ApprovalExpired, nil); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if _, err := svc.Approve(context.Background(), expired.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approve expired error = %v, want ErrNotPending", err)
	}
}

// TestApprove_ConcurrentOnlyOneExecutes 覆盖重复批准：20 个并发 Approve 只有 1 次 executor 调用。
func TestApprove_ConcurrentOnlyOneExecutes(t *testing.T) {
	var ex *fakeExecutor
	release := make(chan struct{})
	ex = &fakeExecutor{result: "ok", onExecute: func() { <-release }}
	svc, _, _ := newService(ex)

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")

	const workers = 20
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Approve(context.Background(), a.ID, "admin")
			errs <- err
		}()
	}
	close(start)
	// 等第一个请求进入 executor 后放行。
	for ex.callCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	close(errs)

	var okCount, conflictCount int
	for err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, ErrNotPending):
			conflictCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || conflictCount != workers-1 {
		t.Fatalf("ok = %d, conflict = %d, want 1/%d", okCount, conflictCount, workers-1)
	}
	if ex.callCount() != 1 {
		t.Fatalf("executor calls = %d, want 1", ex.callCount())
	}
}

// TestReject_ConcurrentWithApprove 验证 Reject 与 Approve 并发时只有一个 CAS 成功。
func TestReject_ConcurrentWithApprove(t *testing.T) {
	ex := &fakeExecutor{result: "ok"}
	svc, _, _ := newService(ex)
	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := svc.Approve(context.Background(), a.ID, "admin")
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-start
		_, err := svc.Reject(context.Background(), a.ID, "admin")
		errs <- err
	}()
	close(start)
	wg.Wait()
	close(errs)

	var okCount, conflictCount int
	for err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, ErrNotPending):
			conflictCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("ok = %d, conflict = %d, want 1/1", okCount, conflictCount)
	}

	got, _ := svc.Get(context.Background(), a.ID)
	switch got.Status {
	case model.ApprovalRejected:
		if ex.callCount() != 0 {
			t.Fatalf("executor calls = %d, want 0 when reject wins", ex.callCount())
		}
	case model.ApprovalApproved, model.ApprovalExecuting, model.ApprovalExecuted:
		if ex.callCount() != 1 {
			t.Fatalf("executor calls = %d, want 1 when approve wins", ex.callCount())
		}
	default:
		t.Fatalf("status = %s", got.Status)
	}
}

// TestApprove_IncidentNotWaitingApproval_ExpiresWithoutExecuting 覆盖需求：
// Incident 不在 WAITING_APPROVAL 时 executor 调用次数为 0，Approval 变为 EXPIRED。
func TestApprove_IncidentNotWaitingApproval_ExpiresWithoutExecuting(t *testing.T) {
	ctx := context.Background()
	ws := newWiredService(t, &fakeExecutor{result: "ok"})

	// 手工写一条绑定到 INVESTIGATING Incident 的 PENDING 审批（不经过 Request，
	// 因此 Incident 不会迁移到 WAITING_APPROVAL）。
	a := &model.Approval{
		ID: "ap_manual", IncidentID: ws.incidentID, RunID: ws.runID, ToolCallID: "call_1",
		Action: "restart_worker", Status: model.ApprovalPending, CreatedAt: time.Now(),
	}
	if err := ws.approvals.Create(ctx, a); err != nil {
		t.Fatalf("create approval: %v", err)
	}

	ex := &fakeExecutor{}
	ws.svc.executor = ex
	if _, err := ws.svc.Approve(ctx, a.ID, "admin"); err == nil {
		t.Fatal("Approve() error = nil, want incident transition error")
	} else if !errors.Is(err, ErrExpired) {
		t.Fatalf("Approve() error = %v, want ErrExpired", err)
	}
	if ex.callCount() != 0 {
		t.Fatalf("executor calls = %d, want 0", ex.callCount())
	}
	stored, _ := ws.approvals.Get(ctx, a.ID)
	if stored.Status != model.ApprovalExpired {
		t.Fatalf("status = %s, want EXPIRED", stored.Status)
	}
	if stored.Error == "" {
		t.Fatal("expired approval should keep the failure reason")
	}
	// Incident 状态未被改动（仍 INVESTIGATING）。
	inc, _ := ws.incidents.Get(ctx, ws.incidentID)
	if inc.Status != model.StatusInvestigating {
		t.Fatalf("incident status = %s, want INVESTIGATING", inc.Status)
	}
}

// TestApprove_MovesIncidentToMitigating 覆盖正常路径：claim 成功后 Incident 进入 MITIGATING。
func TestApprove_MovesIncidentToMitigating(t *testing.T) {
	ctx := context.Background()
	ws := newWiredService(t, &fakeExecutor{result: "ok"})

	a, err := ws.svc.Request(ctx, ws.runID, "call_1", "restart_worker", `{"target":"worker"}`, "consumer down")
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if a.IncidentID != ws.incidentID {
		t.Fatalf("approval incident_id = %q, want %q", a.IncidentID, ws.incidentID)
	}
	inc, _ := ws.incidents.Get(ctx, ws.incidentID)
	if inc.Status != model.StatusWaitingApproval {
		t.Fatalf("incident status after Request = %s, want WAITING_APPROVAL", inc.Status)
	}

	if _, err := ws.svc.Approve(ctx, a.ID, "admin"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	inc, _ = ws.incidents.Get(ctx, ws.incidentID)
	if inc.Status != model.StatusMitigating {
		t.Fatalf("incident status after Approve = %s, want MITIGATING", inc.Status)
	}
}

// TestRequest_RejectsUnknownRunOrIncident 覆盖创建校验：Run / Incident 不存在时拒绝建审批。
func TestRequest_RejectsUnknownRunOrIncident(t *testing.T) {
	ctx := context.Background()
	ws := newWiredService(t, &fakeExecutor{})

	if _, err := ws.svc.Request(ctx, "run_missing", "call_1", "restart_worker", "{}", "r"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Request(unknown run) error = %v, want ErrInvalidRequest", err)
	}
	if _, err := ws.svc.Request(ctx, "", "call_1", "restart_worker", "{}", "r"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Request(empty run) error = %v, want ErrInvalidRequest", err)
	}

	// Run 存在但 Incident 已被删除：拒绝。
	orphanRun := "run_orphan"
	if err := ws.runs.CreateRun(ctx, &model.AgentRun{
		ID: orphanRun, IncidentID: "inc_missing", AgentType: "diagnosis", Status: model.RunRunning, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create orphan run: %v", err)
	}
	if _, err := ws.svc.Request(ctx, orphanRun, "call_1", "restart_worker", "{}", "r"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Request(orphan run) error = %v, want ErrInvalidRequest", err)
	}

	pending, err := ws.approvals.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending approvals = %d, want 0", len(pending))
	}
}

// TestExpirePending 覆盖外部恢复路径：按 Incident 让所有 PENDING 审批过期，且之后批准不会执行。
func TestExpirePending(t *testing.T) {
	ctx := context.Background()
	ex := &fakeExecutor{result: "ok"}
	ws := newWiredService(t, ex)

	first, err := ws.svc.Request(ctx, ws.runID, "call_1", "restart_worker", "{}", "r")
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}

	// 另一个 Incident 的待审批不受影响。
	otherInc, _, err := ws.incidents.Create(ctx, incidentservice.CreateIncidentInput{Service: "other", AlertName: "b", Title: "t"})
	if err != nil {
		t.Fatalf("create other incident: %v", err)
	}
	if _, err := ws.incidents.Analyze(ctx, otherInc.ID); err != nil {
		t.Fatalf("analyze other incident: %v", err)
	}
	otherRun := "run_other"
	if err := ws.runs.CreateRun(ctx, &model.AgentRun{ID: otherRun, IncidentID: otherInc.ID, Status: model.RunRunning, StartedAt: time.Now()}); err != nil {
		t.Fatalf("create other run: %v", err)
	}
	other, err := ws.svc.Request(ctx, otherRun, "call_2", "restart_worker", "{}", "r")
	if err != nil {
		t.Fatalf("Request(other) error = %v", err)
	}

	expired, err := ws.svc.ExpirePending(ctx, ws.incidentID)
	if err != nil {
		t.Fatalf("ExpirePending() error = %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}

	stored, _ := ws.approvals.Get(ctx, first.ID)
	if stored.Status != model.ApprovalExpired {
		t.Fatalf("status = %s, want EXPIRED", stored.Status)
	}
	storedOther, _ := ws.approvals.Get(ctx, other.ID)
	if storedOther.Status != model.ApprovalPending {
		t.Fatalf("other approval status = %s, want PENDING", storedOther.Status)
	}

	// 过期后批准：conflict，且 executor 调用为 0。
	if _, err := ws.svc.Approve(ctx, first.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("Approve(expired) error = %v, want ErrNotPending", err)
	}
	if ex.callCount() != 0 {
		t.Fatalf("executor calls = %d, want 0", ex.callCount())
	}

	if _, err := ws.svc.ExpirePending(ctx, ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ExpirePending(empty) error = %v, want ErrInvalidRequest", err)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc, _, _ := newService(&fakeExecutor{})
	if _, err := svc.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

// seedApproval 直接写一条指定状态的审批。
func seedApproval(t *testing.T, repo *repository.MemoryApproval, id string, status model.ApprovalStatus) *model.Approval {
	t.Helper()
	a := &model.Approval{
		ID: id, IncidentID: "inc_seed", RunID: "run_seed", ToolCallID: "call_seed",
		Action: "restart_worker", Arguments: `{"target":"worker"}`,
		Status: status, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := repo.Create(context.Background(), a); err != nil {
		t.Fatalf("seed approval: %v", err)
	}
	return a
}

func TestApprove_ContextCancellationIsFailedUnlessExecutorMarksUnknown(t *testing.T) {
	svc, repo, _ := newService(nil)
	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ex := &fakeExecutor{err: context.Canceled, onExecute: cancel}
	svc.executor = ex

	got, err := svc.Approve(ctx, a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v, want nil", err)
	}
	if got.Status != model.ApprovalFailed {
		t.Fatalf("status = %s, want FAILED", got.Status)
	}
	stored, _ := repo.Get(context.Background(), a.ID)
	if stored.Status != model.ApprovalFailed || stored.Error == "" {
		t.Fatalf("stored = %+v, want FAILED with error text", stored)
	}
}

func TestApprove_VerificationCancellationResultIsExecuted(t *testing.T) {
	svc, repo, _ := newService(nil)
	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ex := &fakeExecutor{result: `{"status":"verify_failed","verified":false}`, onExecute: cancel}
	svc.executor = ex

	got, err := svc.Approve(ctx, a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v, want persisted action result", err)
	}
	if got.Status != model.ApprovalExecuted {
		t.Fatalf("status = %s, want EXECUTED because the action result is known", got.Status)
	}
	stored, _ := repo.Get(context.Background(), a.ID)
	if stored.Status != model.ApprovalExecuted || stored.Result != ex.result {
		t.Fatalf("stored = %+v, want EXECUTED verification failure result", stored)
	}
}

// deadlineRecordingRepo 验证最终结果使用 executor 返回后新建的持久化期限。
type deadlineRecordingRepo struct {
	*repository.MemoryApproval
	executingDeadline time.Time
}

func (r *deadlineRecordingRepo) TransitionStatus(ctx context.Context, id string, from, to model.ApprovalStatus, mutate func(*model.Approval)) (*model.Approval, error) {
	if from == model.ApprovalApproved && to == model.ApprovalExecuting {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("persistence context has no deadline")
		}
		r.executingDeadline = deadline
	}
	if from == model.ApprovalExecuting {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("persistence context has no deadline")
		}
		if !deadline.After(r.executingDeadline.Add(50 * time.Millisecond)) {
			return nil, errors.New("final persistence deadline was not refreshed after execution")
		}
	}
	return r.MemoryApproval.TransitionStatus(ctx, id, from, to, mutate)
}

func TestApprove_RefreshesPersistenceDeadlineAfterExecution(t *testing.T) {
	repo := &deadlineRecordingRepo{MemoryApproval: repository.NewMemoryApproval()}
	ex := &fakeExecutor{result: "ok", onExecute: func() { time.Sleep(100 * time.Millisecond) }}
	svc := New(repo, runtime.NewStreamBroker(), ex)
	a, err := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	got, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if got.Status != model.ApprovalExecuted {
		t.Fatalf("status = %s, want EXECUTED", got.Status)
	}
}

// ctxAwareApprovalRepo 让取消后的调用像真实数据库一样失败（Memory 本身不检查 ctx）。
type ctxAwareApprovalRepo struct {
	*repository.MemoryApproval
}

func (r *ctxAwareApprovalRepo) TransitionStatus(ctx context.Context, id string, from, to model.ApprovalStatus, mutate func(*model.Approval)) (*model.Approval, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.MemoryApproval.TransitionStatus(ctx, id, from, to, mutate)
}

func TestExpire_CancelledContextStillPersists(t *testing.T) {
	repo := &ctxAwareApprovalRepo{MemoryApproval: repository.NewMemoryApproval()}
	svc := New(repo, runtime.NewStreamBroker(), nil)
	a := seedApproval(t, repo.MemoryApproval, "ap_expire_cancelled", model.ApprovalPending)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.expire(ctx, a, model.ApprovalPending, errors.New("incident transition failed")); !errors.Is(err, ErrExpired) {
		t.Fatalf("expire() error = %v, want ErrExpired", err)
	}
	stored, err := repo.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != model.ApprovalExpired {
		t.Fatalf("status = %s, want EXPIRED", stored.Status)
	}
}

// cancelAfterClaimRepo 在 PENDING -> APPROVED 成功之后立刻取消请求 context，
// 复现"claim 已成功、客户端随即断开"的窗口。
type cancelAfterClaimRepo struct {
	*ctxAwareApprovalRepo
	cancel context.CancelFunc
}

func (r *cancelAfterClaimRepo) TransitionStatus(ctx context.Context, id string, from, to model.ApprovalStatus, mutate func(*model.Approval)) (*model.Approval, error) {
	a, err := r.ctxAwareApprovalRepo.TransitionStatus(ctx, id, from, to, mutate)
	if err == nil && from == model.ApprovalPending && to == model.ApprovalApproved && r.cancel != nil {
		r.cancel()
	}
	return a, err
}

// TestApprove_CancelAfterClaimStillExpires 覆盖"claim 后请求被取消"：
// Incident 迁移失败时，EXPIRED 必须用不可取消的持久化 context 落库，
// 否则审批会永久停在 APPROVED（启动扫描尚未接线时就会真正滞留）。
func TestApprove_CancelAfterClaimStillExpires(t *testing.T) {
	ctx := context.Background()
	ws := newWiredService(t, &fakeExecutor{result: "ok"})

	// Incident 停在 INVESTIGATING：迁移到 MITIGATING 必然失败。
	a := &model.Approval{
		ID: "ap_cancel_after_claim", IncidentID: ws.incidentID, RunID: ws.runID, ToolCallID: "call_1",
		Action: "restart_worker", Arguments: "{}", Status: model.ApprovalPending, CreatedAt: time.Now(),
	}
	if err := ws.approvals.Create(ctx, a); err != nil {
		t.Fatalf("create approval: %v", err)
	}

	reqCtx, cancel := context.WithCancel(ctx)
	repo := &cancelAfterClaimRepo{ctxAwareApprovalRepo: &ctxAwareApprovalRepo{MemoryApproval: ws.approvals}, cancel: cancel}
	ex := &fakeExecutor{result: "ok"}
	svc := New(repo, ws.broker, ex).WithIncidentState(ws.incidents, ws.runs)

	if _, err := svc.Approve(reqCtx, a.ID, "admin"); !errors.Is(err, ErrExpired) {
		t.Fatalf("Approve() error = %v, want ErrExpired", err)
	}
	if reqCtx.Err() == nil {
		t.Fatal("测试前提失败：请求 context 应当已被取消")
	}
	if ex.callCount() != 0 {
		t.Fatalf("executor calls = %d, want 0", ex.callCount())
	}
	stored, err := ws.approvals.Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != model.ApprovalExpired {
		t.Fatalf("status = %s, want EXPIRED（取消后过期仍必须落库）", stored.Status)
	}
	if stored.Error == "" {
		t.Fatal("expired approval should keep the failure reason")
	}
}

func collectEvents(t *testing.T, ch <-chan runtime.StreamEvent, want []string) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	need := map[string]bool{}
	for _, w := range want {
		need[w] = true
	}
	for len(seen) < len(need) {
		ev := <-ch
		seen[ev.Type] = true
	}
	for _, w := range want {
		if !seen[w] {
			t.Fatalf("missing event %s, seen: %v", w, seen)
		}
	}
	return seen
}
