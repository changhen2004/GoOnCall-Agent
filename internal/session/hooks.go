package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"gooncall-agent/internal/agent/core"
	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/tools"
)

// runHooks 实现核心定义的同步回调契约。
//
// 所有关键保存都使用独立的、有上限的持久化上下文：客户端断连或运行取消
// 不能丢弃已经产生的完整消息与工具结果。
type runHooks struct {
	svc  *Service
	run  Run
	mu   sync.Mutex
	exec map[string][]string // tool_call_id -> 执行记录 ID（允许重复 ID）
}

func newRunHooks(svc *Service, run Run) *runHooks {
	return &runHooks{svc: svc, run: run, exec: make(map[string][]string)}
}

// SaveMessages 同步保存完整消息。
func (h *runHooks) SaveMessages(_ context.Context, messages []llm.Message) error {
	ctx, cancel := h.svc.persistContext()
	defer cancel()

	for _, message := range messages {
		record := Message{
			ConversationID: h.run.ConversationID,
			RunID:          h.run.ID,
			Role:           message.Role,
			Content:        message.Content,
			ToolCalls:      message.ToolCalls,
			ToolCallID:     message.ToolCallID,
			Name:           message.Name,
			Status:         MessageComplete,
			CreatedAt:      time.Now(),
		}
		if err := h.svc.store.AppendMessage(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

// SaveDraft 保存未完成文本，不能当作完整回复进入后续上下文。
func (h *runHooks) SaveDraft(_ context.Context, content string) error {
	ctx, cancel := h.svc.persistContext()
	defer cancel()

	err := h.svc.store.AppendMessage(ctx, Message{
		ConversationID: h.run.ConversationID,
		RunID:          h.run.ID,
		Role:           llm.RoleAssistant,
		Content:        content,
		Status:         MessageDraft,
		CreatedAt:      time.Now(),
	})
	if err != nil {
		h.svc.logger.Error("保存未完成草稿失败", "run_id", h.run.ID, "error", err)
	}
	return err
}

// BeginTool 保存工具执行记录；需要审批时等待决定并取得执行权。
func (h *runHooks) BeginTool(ctx context.Context, call llm.ToolCall, tool tools.Tool, args json.RawMessage) error {
	now := time.Now()
	execution := ToolExecution{
		ID:               uuid.NewString(),
		RunID:            h.run.ID,
		ToolCallID:       call.ID,
		ToolName:         tool.Name,
		Arguments:        string(args),
		RequiresApproval: tool.RequiresApproval,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if !tool.RequiresApproval {
		execution.Status = ToolRunning
		if err := h.saveExecution(execution); err != nil {
			return err
		}
		h.remember(call.ID, execution.ID)
		h.emitToolStart(call, tool.Name, string(args))
		return nil
	}

	execution.Status = ToolPendingApproval
	approval := Approval{
		ID:              uuid.NewString(),
		ToolExecutionID: execution.ID,
		RunID:           h.run.ID,
		Status:          ApprovalPending,
		ExpiresAt:       now.Add(h.svc.opts.ApprovalTTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	waiter := &approvalWaiter{approval: approval, decision: make(chan ApprovalStatus, 1)}
	h.svc.registerWaiter(waiter)
	defer h.svc.unregisterWaiter(approval.ID)

	persistCtx, cancel := h.svc.persistContext()
	active, err := h.svc.store.BeginApproval(persistCtx, execution, approval)
	cancel()
	if err != nil {
		return err
	}
	if !active {
		return core.ErrRunSuperseded
	}

	h.remember(call.ID, execution.ID)
	h.svc.emit(h.run.ID, Event{
		Type:       "approval_required",
		ToolCallID: call.ID,
		ToolName:   tool.Name,
		Arguments:  string(args),
		ApprovalID: approval.ID,
		Status:     string(ApprovalPending),
	})

	decision, err := h.svc.awaitApproval(ctx, waiter)
	if err != nil {
		h.abandonExecution(execution.ID, err)
		return err
	}

	switch decision {
	case ApprovalRejected:
		if err := h.resumeRun(); err != nil {
			return err
		}
		// 执行记录与拒绝结果由核心调用 EndTool 原子保存。
		return core.ErrToolRejected

	case ApprovalApproved:
		persistCtx, cancel := h.svc.persistContext()
		claimed, claimErr := h.svc.store.ClaimApproval(persistCtx, approval.ID, time.Now())
		cancel()
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			h.updateExecution(execution.ID, ToolCanceled, "审批失效或运行已结束")
			return core.ErrApprovalExpired
		}
		// 审批绑定校验：执行权对应的必须是保存时的固定工具与参数。
		if call.ID != execution.ToolCallID || tool.Name != execution.ToolName || string(args) != execution.Arguments {
			h.updateExecution(execution.ID, ToolCanceled, "审批绑定不一致")
			return core.ErrRunSuperseded
		}
		h.emitToolStart(call, tool.Name, string(args))
		return nil

	default:
		h.updateExecution(execution.ID, ToolCanceled, "审批状态未知")
		return core.ErrApprovalExpired
	}
}

// EndTool 在同一事务中保存工具执行结果与对应工具消息。
//
// 外部操作已经成功但本地事务失败时，结果无法确认；UI 与查询接口会看到
// 执行记录停在 running，重启处理会把它标为 unknown，不会伪装成普通失败重试。
func (h *runHooks) EndTool(_ context.Context, call llm.ToolCall, status core.ToolStatus, content string) error {
	execID, ok := h.takeExecution(call.ID)
	if !ok {
		return fmt.Errorf("未找到工具 %s 的执行记录", call.Function.Name)
	}

	message := Message{
		ConversationID: h.run.ConversationID,
		RunID:          h.run.ID,
		Role:           llm.RoleTool,
		Content:        content,
		ToolCallID:     call.ID,
		Name:           call.Function.Name,
		Status:         MessageComplete,
		CreatedAt:      time.Now(),
	}

	ctx, cancel := h.svc.persistContext()
	defer cancel()
	if err := h.svc.store.FinishToolExecution(ctx, execID, ToolExecutionStatus(status), content, message); err != nil {
		// 外部操作可能已经成功；尽力把执行记录标为结果未知，便于人工核对。
		if status == core.ToolSucceeded {
			h.updateExecution(execID, ToolUnknown, content)
		}
		return err
	}

	h.svc.emit(h.run.ID, Event{
		Type:       "tool_end",
		ToolCallID: call.ID,
		ToolName:   call.Function.Name,
		Status:     string(status),
		Result:     content,
	})
	return nil
}

func (h *runHooks) saveExecution(execution ToolExecution) error {
	ctx, cancel := h.svc.persistContext()
	defer cancel()
	return h.svc.store.CreateToolExecution(ctx, execution)
}

func (h *runHooks) updateExecution(id string, status ToolExecutionStatus, result string) {
	ctx, cancel := h.svc.persistContext()
	defer cancel()
	if err := h.svc.store.UpdateToolExecution(ctx, id, status, result); err != nil {
		h.svc.logger.Error("更新工具执行记录失败", "execution_id", id, "error", err)
	}
}

func (h *runHooks) abandonExecution(id string, cause error) {
	ctx, cancel := h.svc.persistContext()
	defer cancel()
	_ = h.svc.store.InvalidateApprovals(ctx, h.run.ID)
	_ = h.svc.store.UpdateToolExecution(ctx, id, ToolCanceled, cause.Error())
}

func (h *runHooks) resumeRun() error {
	ctx, cancel := h.svc.persistContext()
	defer cancel()
	active, err := h.svc.store.UpdateRunStatus(ctx, h.run.ID, RunRunning, "", RunWaitingApproval)
	if err != nil {
		return err
	}
	if !active {
		return core.ErrRunSuperseded
	}
	return nil
}

func (h *runHooks) emitToolStart(call llm.ToolCall, name, args string) {
	h.svc.emit(h.run.ID, Event{
		Type:       "tool_start",
		ToolCallID: call.ID,
		ToolName:   name,
		Arguments:  args,
		Status:     string(ToolRunning),
	})
}

func (h *runHooks) remember(callID, execID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.exec[callID] = append(h.exec[callID], execID)
}

func (h *runHooks) takeExecution(callID string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := h.exec[callID]
	if len(ids) == 0 {
		return "", false
	}
	id := ids[0]
	if len(ids) == 1 {
		delete(h.exec, callID)
	} else {
		h.exec[callID] = ids[1:]
	}
	return id, true
}
