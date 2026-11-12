package session

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound 表示记录不存在。
	ErrNotFound = errors.New("记录不存在")
	// ErrForbidden 表示调用方无权操作该会话。
	ErrForbidden = errors.New("无权操作该会话")
	// ErrRunActive 表示会话已有活动运行。
	ErrRunActive = errors.New("会话已有运行中的任务")
	// ErrConflict 表示状态冲突（重复决定、已取消等）。
	ErrConflict = errors.New("状态冲突")
	// ErrInvalid 表示请求参数无效。
	ErrInvalid = errors.New("请求参数无效")
)

// Store 是会话层需要的持久化契约。
//
// 关键写操作在实现中必须使用事务或条件更新，保证并发下不会出现两个活动运行、
// 重复工具执行或丢失工具配对记录。
type Store interface {
	CreateConversation(ctx context.Context, conversation Conversation) error
	Conversation(ctx context.Context, id string) (*Conversation, error)

	// StartRun 在同一事务中检查会话无活动运行、创建 Run 并保存用户消息。
	// 会话忙碌时必须返回 ErrRunActive。
	StartRun(ctx context.Context, run Run, userMessage Message) error
	Run(ctx context.Context, id string) (*Run, error)
	Runs(ctx context.Context, conversationID string) ([]Run, error)
	// ActiveRunsByOwner 返回属于指定新用户的活动运行（用于禁用账号时取消运行）。
	ActiveRunsByOwner(ctx context.Context, ownerID string) ([]Run, error)

	// Messages 返回全部消息（含草稿），用于会话查询。
	Messages(ctx context.Context, conversationID string) ([]Message, error)
	// CompletedHistory 返回已完成且消息完整的运行消息，按运行时间与序号排序。
	CompletedHistory(ctx context.Context, conversationID, excludeRunID string) ([]Message, error)
	AppendMessage(ctx context.Context, message Message) error

	// UpdateRunStatus 条件更新运行状态；from 为空表示无条件。返回是否更新成功。
	UpdateRunStatus(ctx context.Context, runID string, to RunStatus, reason string, from ...RunStatus) (bool, error)

	CreateToolExecution(ctx context.Context, execution ToolExecution) error
	UpdateToolExecution(ctx context.Context, id string, status ToolExecutionStatus, result string) error
	// FinishToolExecution 在同一事务中更新工具执行结果并保存对应工具消息。
	FinishToolExecution(ctx context.Context, id string, status ToolExecutionStatus, result string, message Message) error
	// BeginApproval 创建待审批工具执行与审批记录，并把运行置为 waiting_approval。
	// 运行已不在活动状态时返回 false。
	BeginApproval(ctx context.Context, execution ToolExecution, approval Approval) (bool, error)
	// ClaimApproval 原子取得执行权：审批必须已批准且未过期，运行必须仍在等待审批。
	// 成功时把运行恢复为 running。
	ClaimApproval(ctx context.Context, approvalID string, now time.Time) (bool, error)
	ExpireApproval(ctx context.Context, approvalID string, now time.Time) (bool, error)
	DecideApproval(ctx context.Context, approvalID string, status ApprovalStatus, operator string, now time.Time) (bool, error)
	InvalidateApprovals(ctx context.Context, runID string) error
	Approval(ctx context.Context, id string) (*Approval, error)
	PendingApprovals(ctx context.Context, runID string) ([]Approval, error)
	ToolExecutions(ctx context.Context, runID string) ([]ToolExecution, error)

	// InterruptStale 在取得进程独占权后处理旧进程遗留的运行。
	InterruptStale(ctx context.Context, now time.Time) (InterruptReport, error)
	Close() error
}
