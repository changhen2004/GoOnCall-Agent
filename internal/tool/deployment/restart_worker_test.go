package deployment

import (
	"context"
	"strings"
	"testing"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/execution/policy"
	"gooncall-agent/internal/incident/repository"
)

func newTestTool(approvalEnabled bool) (*Tool, *repository.MemoryApproval) {
	approvalRepo := repository.NewMemoryApproval()
	broker := agentruntime.NewStreamBroker()
	approvalSvc := approval.New(approvalRepo, broker, nil)
	return New(approvalSvc, policy.New(approvalEnabled)), approvalRepo
}

func TestRequest_CreatesPendingApproval(t *testing.T) {
	tool, approvalRepo := newTestTool(true)
	einoTool, err := tool.EinoTool()
	if err != nil {
		t.Fatalf("EinoTool() error = %v", err)
	}

	ctx := agentruntime.WithRunID(context.Background(), "run_1")
	out, err := einoTool.InvokableRun(ctx, `{"target":"resource-community-worker","reason":"consumer count dropped"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, "pending_approval") {
		t.Fatalf("output = %s, want pending_approval", out)
	}

	pending, _ := approvalRepo.ListPending(context.Background())
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	if pending[0].Action != "restart_worker" || pending[0].RunID != "run_1" {
		t.Fatalf("approval = %+v", pending[0])
	}
}

func TestRequest_DeniedWhenApprovalDisabled(t *testing.T) {
	tool, _ := newTestTool(false)
	einoTool, _ := tool.EinoTool()

	ctx := agentruntime.WithRunID(context.Background(), "run_1")
	if _, err := einoTool.InvokableRun(ctx, `{"target":"worker"}`); err == nil {
		t.Fatal("expected deny error when approval disabled")
	}
}

func TestExecute(t *testing.T) {
	tool, _ := newTestTool(true)
	out, err := tool.Execute(context.Background(), "restart_worker", `{"target":"worker"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out, "completed") || !strings.Contains(out, "worker") {
		t.Fatalf("output = %s", out)
	}
}

func TestExecute_UnknownAction(t *testing.T) {
	tool, _ := newTestTool(true)
	if _, err := tool.Execute(context.Background(), "delete_queue", "{}"); err == nil {
		t.Fatal("expected error for unknown action")
	}
}
