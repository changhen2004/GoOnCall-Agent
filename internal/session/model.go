// Package session 负责会话身份、历史加载、运行协调、审批与持久化。
package session

import (
	"time"

	"gooncall-agent/internal/llm"
)

// 会话归属类型：user 为新账号的稳定用户 ID，legacy 为旧固定 Token 操作者。
const (
	OwnerKindUser   = "user"
	OwnerKindLegacy = "legacy"
)

// Conversation 是会话身份。新会话的 owner 是稳定用户 ID。
type Conversation struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	OwnerKind string    `json:"owner_kind"`
	CreatedAt time.Time `json:"created_at"`
}

// RunStatus 是一次运行的状态。
type RunStatus string

const (
	RunRunning         RunStatus = "running"
	RunWaitingApproval RunStatus = "waiting_approval"
	RunCompleted       RunStatus = "completed"
	RunFailed          RunStatus = "failed"
	RunCanceled        RunStatus = "canceled"
	RunTimeout         RunStatus = "timeout"
	RunInterrupted     RunStatus = "interrupted"
	RunUnknown         RunStatus = "unknown"
)

// Terminal 判断是否终态。
func (s RunStatus) Terminal() bool {
	switch s {
	case RunRunning, RunWaitingApproval:
		return false
	default:
		return true
	}
}

// ActiveRunStatuses 返回会占用会话的状态。
func ActiveRunStatuses() []RunStatus {
	return []RunStatus{RunRunning, RunWaitingApproval}
}

// Run 对应一次用户输入引发的完整执行轮次。
type Run struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Model          string    `json:"model"`
	Status         RunStatus `json:"status"`
	BudgetMS       int64     `json:"budget_ms"`
	Reason         string    `json:"reason,omitempty"`
	ProcessID      string    `json:"process_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// MessageStatus 区分完整消息与未完成草稿。
type MessageStatus string

const (
	MessageComplete MessageStatus = "complete"
	MessageDraft    MessageStatus = "draft"
)

// Message 是持久化的会话消息。
type Message struct {
	ID             int64          `json:"id"`
	ConversationID string         `json:"conversation_id"`
	RunID          string         `json:"run_id"`
	Seq            int            `json:"seq"`
	Role           llm.Role       `json:"role"`
	Content        string         `json:"content"`
	ToolCalls      []llm.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID     string         `json:"tool_call_id,omitempty"`
	Name           string         `json:"name,omitempty"`
	Status         MessageStatus  `json:"status"`
	CreatedAt      time.Time      `json:"created_at"`
}

// LLM 转换为模型消息。
func (m Message) LLM() llm.Message {
	return llm.Message{
		Role:       m.Role,
		Content:    m.Content,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
}

// ToolExecutionStatus 是工具执行状态。
type ToolExecutionStatus string

const (
	ToolRunning         ToolExecutionStatus = "running"
	ToolPendingApproval ToolExecutionStatus = "pending_approval"
	ToolSucceeded       ToolExecutionStatus = "succeeded"
	ToolFailed          ToolExecutionStatus = "failed"
	ToolRejected        ToolExecutionStatus = "rejected"
	ToolCanceled        ToolExecutionStatus = "canceled"
	ToolUnknown         ToolExecutionStatus = "unknown"
)

// ToolExecution 记录一次工具执行及其固定参数。
type ToolExecution struct {
	ID               string              `json:"id"`
	RunID            string              `json:"run_id"`
	ToolCallID       string              `json:"tool_call_id"`
	ToolName         string              `json:"tool_name"`
	Arguments        string              `json:"arguments"`
	RequiresApproval bool                `json:"requires_approval"`
	Status           ToolExecutionStatus `json:"status"`
	Result           string              `json:"result,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

// ApprovalStatus 是审批状态。
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
	ApprovalInvalid  ApprovalStatus = "invalid"
)

// Approval 绑定一次具体的工具执行。
type Approval struct {
	ID              string         `json:"id"`
	ToolExecutionID string         `json:"tool_execution_id"`
	RunID           string         `json:"run_id"`
	Status          ApprovalStatus `json:"status"`
	Operator        string         `json:"operator,omitempty"`
	ExpiresAt       time.Time      `json:"expires_at"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// InterruptReport 是重启时处理遗留运行的结果统计。
type InterruptReport struct {
	Runs           int64
	Approvals      int64
	ToolExecutions int64
}
