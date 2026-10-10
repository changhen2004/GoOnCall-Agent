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
	if !strings.Contains(out.Output, "completed") || !strings.Contains(out.Output, "worker") {
		t.Fatalf("output = %s", out.Output)
	}
}

func TestExecute_UnknownAction(t *testing.T) {
	tool, _ := newTestTool(true)
	if _, err := tool.Execute(context.Background(), "delete_queue", "{}"); err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// TestRequest_TargetWhitelist 白名单外的 target 与空 target 必须拒绝，不创建审批。
func TestRequest_TargetWhitelist(t *testing.T) {
	tool, approvalRepo := newTestTool(true)
	tool.WithAllowedTargets([]string{"resource-community-worker"})
	einoTool, err := tool.EinoTool()
	if err != nil {
		t.Fatalf("EinoTool() error = %v", err)
	}
	ctx := agentruntime.WithRunID(context.Background(), "run_1")

	if _, err := einoTool.InvokableRun(ctx, `{"target":"some-other-worker"}`); err == nil {
		t.Fatal("unknown target should be rejected")
	}
	if _, err := einoTool.InvokableRun(ctx, `{"target":""}`); err == nil {
		t.Fatal("empty target should be rejected")
	}
	pending, _ := approvalRepo.ListPending(context.Background())
	if len(pending) != 0 {
		t.Fatalf("pending approvals = %d, want 0 (rejected targets must not create approvals)", len(pending))
	}

	if _, err := einoTool.InvokableRun(ctx, `{"target":"resource-community-worker"}`); err != nil {
		t.Fatalf("allowed target rejected: %v", err)
	}
	if pending, _ := approvalRepo.ListPending(context.Background()); len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
}

// TestExecute_TargetWhitelist 执行前也要校验白名单：审批可能先于白名单变更创建。
func TestExecute_TargetWhitelist(t *testing.T) {
	tool, _ := newTestTool(true)
	tool.WithAllowedTargets([]string{"allowed-worker"})

	if _, err := tool.Execute(context.Background(), "restart_worker", `{"target":"other"}`); err == nil {
		t.Fatal("Execute() with unknown target should fail")
	}
	if _, err := tool.Execute(context.Background(), "restart_worker", `{"target":""}`); err == nil {
		t.Fatal("Execute() with empty target should fail")
	}
	if _, err := tool.Execute(context.Background(), "restart_worker", `{"target":"allowed-worker"}`); err != nil {
		t.Fatalf("Execute() with allowed target failed: %v", err)
	}
}

// TestRequest_PropagatesToolCallID ToolCall ID 必须从 context 传入审批，保证审计链一致。
func TestRequest_PropagatesToolCallID(t *testing.T) {
	tool, approvalRepo := newTestTool(true)
	einoTool, _ := tool.EinoTool()

	ctx := agentruntime.WithToolCallID(agentruntime.WithRunID(context.Background(), "run_1"), "call_42")
	if _, err := einoTool.InvokableRun(ctx, `{"target":"worker"}`); err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	pending, _ := approvalRepo.ListPending(context.Background())
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %d, want 1", len(pending))
	}
	if pending[0].ToolCallID != "call_42" {
		t.Fatalf("approval tool_call_id = %q, want call_42", pending[0].ToolCallID)
	}
}

// TestRequest_HTTPExecutorWithoutWhitelist 装配了 HTTP executor 却漏配白名单时必须拒绝，
// 不能 fail-open 让任意 target 触发真实处置。
func TestRequest_HTTPExecutorWithoutWhitelist(t *testing.T) {
	tool, approvalRepo := newTestTool(true)
	tool.WithHTTPExecutor(&HTTPExecutor{url: "http://restart.invalid"})
	einoTool, _ := tool.EinoTool()

	ctx := agentruntime.WithRunID(context.Background(), "run_1")
	if _, err := einoTool.InvokableRun(ctx, `{"target":"any-worker"}`); err == nil {
		t.Fatal("HTTP executor without a whitelist must refuse every target")
	}
	if pending, _ := approvalRepo.ListPending(context.Background()); len(pending) != 0 {
		t.Fatalf("pending approvals = %d, want 0", len(pending))
	}
}

// TestExecute_HTTPExecutorWithoutWhitelist 执行路径同样不能 fail-open。
func TestExecute_HTTPExecutorWithoutWhitelist(t *testing.T) {
	tool, _ := newTestTool(true)
	tool.WithHTTPExecutor(&HTTPExecutor{url: "http://restart.invalid"})

	if _, err := tool.Execute(context.Background(), "restart_worker", `{"target":"any-worker"}`); err == nil {
		t.Fatal("Execute() with HTTP executor and no whitelist must fail")
	}
}

// TestExecute_EmptyWhitelistDeniesAll 显式给空白名单 = 禁止任何 target。
func TestExecute_EmptyWhitelistDeniesAll(t *testing.T) {
	tool, _ := newTestTool(true)
	tool.WithAllowedTargets([]string{})

	if _, err := tool.Execute(context.Background(), "restart_worker", `{"target":"worker"}`); err == nil {
		t.Fatal("Execute() with an empty whitelist must deny every target")
	}
}
