package session

import (
	"context"
	"time"

	"gooncall-agent/internal/agent/core"
)

// approvalWaiter 把审批决定从 HTTP 入口转交给正在等待的运行。
type approvalWaiter struct {
	approval Approval
	decision chan ApprovalStatus
}

func (s *Service) registerWaiter(waiter *approvalWaiter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiters[waiter.approval.ID] = waiter
}

func (s *Service) unregisterWaiter(approvalID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.waiters, approvalID)
}

func (s *Service) notifyWaiter(approvalID string, status ApprovalStatus) {
	s.mu.Lock()
	waiter := s.waiters[approvalID]
	s.mu.Unlock()
	if waiter == nil {
		return
	}
	select {
	case waiter.decision <- status:
	default:
	}
}

// awaitApproval 等待用户决定、取消或审批过期。
//
// 调用方的执行预算已在等待前暂停，审批使用独立的有效期。
func (s *Service) awaitApproval(ctx context.Context, waiter *approvalWaiter) (ApprovalStatus, error) {
	timer := time.NewTimer(time.Until(waiter.approval.ExpiresAt))
	defer timer.Stop()

	select {
	case decision := <-waiter.decision:
		return decision, nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
	}

	persistCtx, cancel := s.persistContext()
	defer cancel()

	expired, err := s.store.ExpireApproval(persistCtx, waiter.approval.ID, time.Now())
	if err != nil {
		return "", err
	}
	if !expired {
		// 到期与批准并发：以数据库中的最终决定为准。
		if approval, err := s.store.Approval(persistCtx, waiter.approval.ID); err == nil {
			switch approval.Status {
			case ApprovalApproved:
				return ApprovalApproved, nil
			case ApprovalRejected:
				return ApprovalRejected, nil
			}
		}
	}

	return ApprovalExpired, core.ErrApprovalExpired
}

// Decide 记录审批决定并唤醒等待中的运行；不直接执行工具。
// 重复决定、过期决定由存储层条件更新拒绝。
func (s *Service) Decide(ctx context.Context, actor, approvalID string, approve bool) error {
	approval, err := s.store.Approval(ctx, approvalID)
	if err != nil {
		return err
	}
	run, err := s.store.Run(ctx, approval.RunID)
	if err != nil {
		return err
	}
	if _, err := s.owned(ctx, actor, run.ConversationID); err != nil {
		return err
	}

	status := ApprovalRejected
	if approve {
		status = ApprovalApproved
	}
	updated, err := s.store.DecideApproval(ctx, approvalID, status, actor, time.Now())
	if err != nil {
		return err
	}
	if !updated {
		// 已过期、已决定或运行已取消。
		return ErrConflict
	}

	s.notifyWaiter(approvalID, status)
	s.emit(run.ID, Event{
		Type:       "approval_decided",
		ApprovalID: approvalID,
		Status:     string(status),
	})
	return nil
}
