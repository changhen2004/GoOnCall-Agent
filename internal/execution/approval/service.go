// Package approval 提供人工审批服务。
package approval

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/observability/metrics"
)

var (
	// ErrNotFound 复用 repository 的未找到错误。
	ErrNotFound = repository.ErrNotFound
	// ErrNotPending 表示审批已处理，无法再次处理。
	ErrNotPending = errors.New("approval is not pending")
)

// ActionExecutor 执行已批准的动作。runID 用于关联 Agent Run 与 Incident。
type ActionExecutor interface {
	Execute(ctx context.Context, action, arguments, runID string) (string, error)
}

// Service 是审批服务，负责请求、批准、拒绝并发布事件。
type Service struct {
	repo        repository.ApprovalRepository
	broker      *agentruntime.StreamBroker
	executor    ActionExecutor
	incidentSvc *incidentservice.Service
	runRepo     repository.RunRepository
}

// New 创建审批服务。
func New(repo repository.ApprovalRepository, broker *agentruntime.StreamBroker, executor ActionExecutor) *Service {
	return &Service{repo: repo, broker: broker, executor: executor}
}

// WithIncidentState 注入 Incident 状态管理（用于审批状态闭环迁移）。
func (s *Service) WithIncidentState(incidentSvc *incidentservice.Service, runRepo repository.RunRepository) *Service {
	s.incidentSvc = incidentSvc
	s.runRepo = runRepo
	return s
}

// moveIncident 根据 runID 解析 incident 并迁移状态（未配置时静默跳过）。
func (s *Service) moveIncident(ctx context.Context, runID string, next model.Status) {
	if s.incidentSvc == nil || s.runRepo == nil || runID == "" {
		return
	}
	run, err := s.runRepo.GetRun(ctx, runID)
	if err != nil {
		return
	}
	_, _ = s.incidentSvc.MoveTo(ctx, run.IncidentID, next)
}

// Request 创建一条 PENDING 审批并发布 approval.required 事件。
func (s *Service) Request(ctx context.Context, runID, toolCallID, action, arguments, reason string) (*model.Approval, error) {
	a := &model.Approval{
		ID:         "ap_" + uuid.NewString(),
		RunID:      runID,
		ToolCallID: toolCallID,
		Action:     action,
		Arguments:  arguments,
		Reason:     reason,
		Status:     model.ApprovalPending,
		CreatedAt:  time.Now(),
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return nil, err
	}
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalPending)).Inc()
	s.moveIncident(ctx, runID, model.StatusWaitingApproval)
	s.publish(a.RunID, "approval.required", map[string]any{"approval_id": a.ID, "action": a.Action})
	return a, nil
}

// Approve 批准审批并执行动作。
func (s *Service) Approve(ctx context.Context, id, approvedBy string) (*model.Approval, error) {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Status != model.ApprovalPending {
		return nil, ErrNotPending
	}

	now := time.Now()
	a.Status = model.ApprovalApproved
	a.ApprovedBy = approvedBy
	a.ApprovedAt = &now
	if err := s.repo.Update(ctx, a); err != nil {
		return nil, err
	}
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalApproved)).Inc()
	s.moveIncident(ctx, a.RunID, model.StatusMitigating)
	s.publish(a.RunID, "action.approved", map[string]any{"approval_id": a.ID, "action": a.Action})

	if s.executor != nil {
		result, execErr := s.executor.Execute(ctx, a.Action, a.Arguments, a.RunID)
		if execErr != nil {
			s.publish(a.RunID, "action.completed", map[string]any{"approval_id": a.ID, "status": "FAILED", "error": execErr.Error()})
		} else {
			s.publish(a.RunID, "action.completed", map[string]any{"approval_id": a.ID, "status": "COMPLETED", "result": result})
		}
	}
	return a, nil
}

// Reject 拒绝审批。
func (s *Service) Reject(ctx context.Context, id, rejectedBy string) (*model.Approval, error) {
	a, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a.Status != model.ApprovalPending {
		return nil, ErrNotPending
	}

	now := time.Now()
	a.Status = model.ApprovalRejected
	a.ApprovedBy = rejectedBy
	a.ApprovedAt = &now
	if err := s.repo.Update(ctx, a); err != nil {
		return nil, err
	}
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalRejected)).Inc()
	s.moveIncident(ctx, a.RunID, model.StatusFailed)
	s.publish(a.RunID, "action.rejected", map[string]any{"approval_id": a.ID, "action": a.Action})
	return a, nil
}

// Get 查询审批。
func (s *Service) Get(ctx context.Context, id string) (*model.Approval, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) publish(runID, typ string, data map[string]any) {
	if s.broker != nil {
		s.broker.Publish(runID, agentruntime.StreamEvent{Type: typ, Data: data})
	}
}
