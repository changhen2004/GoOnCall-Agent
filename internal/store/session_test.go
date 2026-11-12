package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/session"
)

// TestSessionStoreLifecycle 覆盖单活动运行约束、历史筛选、审批执行权与重启中断。
func TestSessionStoreLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	now := time.Now()

	if err := db.CreateConversation(ctx, session.Conversation{ID: "c1", Owner: "alice", CreatedAt: now}); err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	run := session.Run{
		ID: "r1", ConversationID: "c1", Model: "default",
		Status: session.RunRunning, ProcessID: "p1", CreatedAt: now, UpdatedAt: now,
	}
	user := session.Message{
		ConversationID: "c1", RunID: "r1", Role: llm.RoleUser,
		Content: "你好", Status: session.MessageComplete, CreatedAt: now,
	}
	if err := db.StartRun(ctx, run, user); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	second := session.Run{
		ID: "r2", ConversationID: "c1", Model: "default",
		Status: session.RunRunning, ProcessID: "p1", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.StartRun(ctx, second, session.Message{ConversationID: "c1", RunID: "r2", Role: llm.RoleUser, Content: "并发"}); !errors.Is(err, session.ErrRunActive) {
		t.Fatalf("并发运行错误 = %v", err)
	}

	history, err := db.CompletedHistory(ctx, "c1", "")
	if err != nil {
		t.Fatalf("CompletedHistory: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("活动运行不应进入历史，实际 %d 条", len(history))
	}

	updated, err := db.UpdateRunStatus(ctx, "r1", session.RunCompleted, "", session.ActiveRunStatuses()...)
	if err != nil || !updated {
		t.Fatalf("UpdateRunStatus = %v, %v", updated, err)
	}
	history, err = db.CompletedHistory(ctx, "c1", "")
	if err != nil || len(history) != 1 {
		t.Fatalf("完成后的历史 = %d, %v", len(history), err)
	}

	// 未完成草稿不能进入模型上下文。
	if err := db.AppendMessage(ctx, session.Message{
		ConversationID: "c1", RunID: "r1", Role: llm.RoleAssistant,
		Content: "半截草稿", Status: session.MessageDraft, CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	history, err = db.CompletedHistory(ctx, "c1", "")
	if err != nil || len(history) != 1 {
		t.Fatalf("草稿不应进入历史，实际 %d 条, %v", len(history), err)
	}

	// 上一条运行结束后，会话可以启动新的运行。
	run2 := session.Run{
		ID: "r2", ConversationID: "c1", Model: "default",
		Status: session.RunRunning, ProcessID: "p1", CreatedAt: now, UpdatedAt: now,
	}
	user2 := session.Message{
		ConversationID: "c1", RunID: "r2", Role: llm.RoleUser,
		Content: "第二条", Status: session.MessageComplete, CreatedAt: now,
	}
	if err := db.StartRun(ctx, run2, user2); err != nil {
		t.Fatalf("第二次 StartRun: %v", err)
	}

	// 审批流程：等待 → 批准 → 取得执行权。
	execution := session.ToolExecution{
		ID: "e1", RunID: "r2", ToolCallID: "call_1", ToolName: "demo_write_file",
		Arguments: `{"path":"a.txt","content":"hi"}`, RequiresApproval: true,
		Status: session.ToolPendingApproval, CreatedAt: now, UpdatedAt: now,
	}
	approval := session.Approval{
		ID: "a1", ToolExecutionID: "e1", RunID: "r2", Status: session.ApprovalPending,
		ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now,
	}
	active, err := db.BeginApproval(ctx, execution, approval)
	if err != nil || !active {
		t.Fatalf("BeginApproval = %v, %v", active, err)
	}
	waiting, err := db.Run(ctx, "r2")
	if err != nil || waiting.Status != session.RunWaitingApproval {
		t.Fatalf("等待审批状态 = %+v, %v", waiting, err)
	}

	decided, err := db.DecideApproval(ctx, "a1", session.ApprovalApproved, "alice", time.Now())
	if err != nil || !decided {
		t.Fatalf("DecideApproval = %v, %v", decided, err)
	}
	if again, _ := db.DecideApproval(ctx, "a1", session.ApprovalApproved, "alice", time.Now()); again {
		t.Fatal("重复批准不应成功")
	}

	claimed, err := db.ClaimApproval(ctx, "a1", time.Now())
	if err != nil || !claimed {
		t.Fatalf("ClaimApproval = %v, %v", claimed, err)
	}
	if again, _ := db.ClaimApproval(ctx, "a1", time.Now()); again {
		t.Fatal("重复取得执行权不应成功")
	}

	// 重启处理：活动运行中断，已开始的写操作标记结果未知。
	report, err := db.InterruptStale(ctx, time.Now())
	if err != nil {
		t.Fatalf("InterruptStale: %v", err)
	}
	if report.Runs != 1 {
		t.Fatalf("中断运行数 = %d", report.Runs)
	}
	interrupted, err := db.Run(ctx, "r2")
	if err != nil || interrupted.Status != session.RunInterrupted {
		t.Fatalf("中断状态 = %+v, %v", interrupted, err)
	}
	executions, err := db.ToolExecutions(ctx, "r2")
	if err != nil || len(executions) != 1 {
		t.Fatalf("工具执行记录 = %+v, %v", executions, err)
	}
	if executions[0].Status != session.ToolUnknown {
		t.Fatalf("已开始写操作状态 = %s", executions[0].Status)
	}
}
