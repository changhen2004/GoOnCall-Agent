package repository

import (
	"context"

	"gooncall-agent/internal/incident/model"
)

// ApprovalRepository 是审批持久化抽象。
//
// 状态变更只能走 TransitionStatus（CAS），不提供无条件的 Update：
// 否则调用方可以绕过"同一 approval 只允许一个请求进入执行路径"的约束。
type ApprovalRepository interface {
	Create(ctx context.Context, a *model.Approval) error
	Get(ctx context.Context, id string) (*model.Approval, error)
	ListByRun(ctx context.Context, runID string) ([]*model.Approval, error)
	ListPending(ctx context.Context) ([]*model.Approval, error)

	// TransitionStatus 原子条件更新（执行权 CAS）：仅当记录当前状态等于 from 时，
	// 把状态改为 to 并应用 mutate，返回更新后的记录。
	//
	//	状态不匹配（并发批准/已拒绝/已过期）-> ErrConflict
	//	记录不存在 -> ErrNotFound
	//
	// 支持的状态迁移（上层使用）：
	//	PENDING  -> APPROVED / EXECUTING / REJECTED / EXPIRED
	//	APPROVED -> EXECUTING
	//	EXECUTING-> EXECUTED / FAILED / UNKNOWN / EXPIRED
	TransitionStatus(ctx context.Context, id string, from, to model.ApprovalStatus, mutate func(*model.Approval)) (*model.Approval, error)

	// ExpirePendingByIncident 把某个 Incident 下所有 PENDING 审批置为 EXPIRED，返回条数。
	// 供外部恢复（Alertmanager resolved）路径使用：恢复后旧审批不得再执行。
	ExpirePendingByIncident(ctx context.Context, incidentID string) (int, error)
}
