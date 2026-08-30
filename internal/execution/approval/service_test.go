package approval

import (
	"context"
	"errors"
	"testing"

	"gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
)

type fakeExecutor struct {
	result string
	err    error
	called bool
}

func (f *fakeExecutor) Execute(_ context.Context, action, arguments, runID string) (string, error) {
	f.called = true
	return f.result, f.err
}

func newService(executor ActionExecutor) (*Service, *repository.MemoryApproval, *runtime.StreamBroker) {
	repo := repository.NewMemoryApproval()
	broker := runtime.NewStreamBroker()
	return New(repo, broker, executor), repo, broker
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
	if !ex.called {
		t.Fatal("executor should be called on approve")
	}

	// 事件
	ch, cancel := broker.Subscribe("run_1")
	defer cancel()
	seen := collectEvents(t, ch, []string{"approval.required", "action.approved", "action.executing", "action.completed"})
	_ = seen
}

// recordingApprovalRepo 记录每次 Update 落库的状态，验证审计状态链。
type recordingApprovalRepo struct {
	*repository.MemoryApproval
	statuses []model.ApprovalStatus
}

func (r *recordingApprovalRepo) Update(ctx context.Context, a *model.Approval) error {
	if err := r.MemoryApproval.Update(ctx, a); err != nil {
		return err
	}
	r.statuses = append(r.statuses, a.Status)
	return nil
}

func TestApprove_StateChain(t *testing.T) {
	repo := &recordingApprovalRepo{MemoryApproval: repository.NewMemoryApproval()}
	svc := New(repo, runtime.NewStreamBroker(), &fakeExecutor{result: "ok"})

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	_, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
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
	ex := &fakeExecutor{err: errors.New("restart failed")}
	svc, _, _ := newService(ex)

	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	got, err := svc.Approve(context.Background(), a.ID, "admin")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if got.Status != model.ApprovalFailed {
		t.Fatalf("status = %s, want FAILED", got.Status)
	}
	if !ex.called {
		t.Fatal("executor should be called")
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
	if ex.called {
		t.Fatal("executor should NOT be called on reject")
	}
}

func TestApprove_NotPending(t *testing.T) {
	svc, repo, _ := newService(&fakeExecutor{})
	a, _ := svc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{}", "r")
	_, _ = svc.Approve(context.Background(), a.ID, "admin")

	if _, err := svc.Approve(context.Background(), a.ID, "admin"); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second approve error = %v, want ErrNotPending", err)
	}
	_ = repo
}

func TestGet_NotFound(t *testing.T) {
	svc, _, _ := newService(&fakeExecutor{})
	if _, err := svc.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
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
