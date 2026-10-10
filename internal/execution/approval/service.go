// Package approval 提供人工审批服务。
package approval

import (
	"context"
	"errors"
	"fmt"
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
	// ErrNotPending 表示审批已被处理（重复批准、已拒绝、已过期、并发抢占执行权）。
	ErrNotPending = errors.New("approval is not pending")
	// ErrInvalidRequest 表示创建审批的入参无法关联到 Run/Incident。
	ErrInvalidRequest = errors.New("invalid approval request")
	// ErrExpired 表示审批在取得执行权后因状态不再允许执行而被置为 EXPIRED
	// （Incident 已外部恢复或迁移失败）；执行器未被调用。
	ErrExpired = errors.New("approval expired")
	// ErrResultUnknown 由 ActionExecutor 包裹后返回，表示外部动作超时/结果未知：
	// Approval 落为 UNKNOWN，禁止把它当作明确失败后自动重试。
	ErrResultUnknown = errors.New("action result unknown")
)

// ActionExecutor 执行已批准的动作。runID 用于关联 Agent Run 与 Incident。
//
// 执行契约（Agent D 实现 HTTP 适配器时必须遵守）：
//   - 幂等键：approval_id 通过 WithApprovalID(ctx, ...) 注入，执行器用
//     ApprovalIDFrom(ctx) 取出并作为 HTTP 头 Idempotency-Key 发送；
//   - 结果未知：超时/无法确认是否已执行时，返回 errors.Join/包裹 ErrResultUnknown 的错误；
//   - OperationID：成功时通过 ExecutionResult.OperationID 返回，并持久化为 Approval.OperationID；
//   - 错误文本不得包含 Token 或 Authorization header。
type ActionExecutor interface {
	Execute(ctx context.Context, action, arguments, runID string) (ExecutionResult, error)
}

// ExecutionResult 是 ActionExecutor 与审批/观测层的共享结果。
type ExecutionResult struct {
	Output      string
	OperationID string
	IncidentID  string
	Verified    bool
}

type approvalIDCtxKey struct{}

// WithApprovalID 把审批 ID（外部处置幂等键）放入 context 传给 ActionExecutor。
func WithApprovalID(ctx context.Context, approvalID string) context.Context {
	return context.WithValue(ctx, approvalIDCtxKey{}, approvalID)
}

// ApprovalIDFrom 读取当前执行对应的 approval_id（不存在时返回空串）。
func ApprovalIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(approvalIDCtxKey{}).(string)
	return v
}

// persistTimeout 是"外部动作可能已经发生"之后落库的超时上限：
// 既不能因为客户端取消而丢结果，也不能让后台写入无限挂起。
const persistTimeout = 5 * time.Second

// persistContext 返回不受客户端取消影响的落库 context（保留取值 + 短超时）。
func persistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
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

// WithIncidentState 注入 Incident 状态管理（用于审批状态闭环迁移与 Run->Incident 解析）。
func (s *Service) WithIncidentState(incidentSvc *incidentservice.Service, runRepo repository.RunRepository) *Service {
	s.incidentSvc = incidentSvc
	s.runRepo = runRepo
	return s
}

// resolveIncidentID 通过 Run 解析 Incident：注入 Run 仓库后强制校验 Run 与 Incident 存在。
// 未注入（单元测试 / 无持久化演示）时返回空串，保持旧行为。
func (s *Service) resolveIncidentID(ctx context.Context, runID string) (string, error) {
	if s.runRepo == nil {
		return "", nil
	}
	if runID == "" {
		return "", fmt.Errorf("%w: run_id is required to bind an approval", ErrInvalidRequest)
	}
	run, err := s.runRepo.GetRun(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("%w: run %s: %v", ErrInvalidRequest, runID, err)
	}
	if s.incidentSvc != nil {
		if _, err := s.incidentSvc.Get(ctx, run.IncidentID); err != nil {
			return "", fmt.Errorf("%w: incident %s of run %s: %v", ErrInvalidRequest, run.IncidentID, runID, err)
		}
	}
	return run.IncidentID, nil
}

// approvalIncidentID 解析审批所属 Incident：优先用创建时固化的 IncidentID，
// 缺失时（旧记录）通过 Run 反查。未装配状态闭环时返回空串。
func (s *Service) approvalIncidentID(ctx context.Context, a *model.Approval) (string, error) {
	if s.incidentSvc == nil {
		return "", nil
	}
	if a.IncidentID != "" {
		return a.IncidentID, nil
	}
	if s.runRepo == nil || a.RunID == "" {
		return "", fmt.Errorf("approval %s is not bound to any incident", a.ID)
	}
	run, err := s.runRepo.GetRun(ctx, a.RunID)
	if err != nil {
		return "", fmt.Errorf("resolve incident of approval %s: %w", a.ID, err)
	}
	return run.IncidentID, nil
}

// moveIncident 把审批所属 Incident 迁移到 next；未装配状态闭环时跳过。
// 迁移失败必须返回错误，由调用方决定是否让审批失效。
func (s *Service) moveIncident(ctx context.Context, a *model.Approval, next model.Status) error {
	incidentID, err := s.approvalIncidentID(ctx, a)
	if err != nil || incidentID == "" {
		return err
	}
	if _, err := s.incidentSvc.MoveTo(ctx, incidentID, next); err != nil {
		return fmt.Errorf("move incident %s to %s: %w", incidentID, next, err)
	}
	return nil
}

// Request 创建一条 PENDING 审批并发布 approval.required 事件。
// IncidentID 由 Run 解析并固化；Run/Incident 不存在时拒绝创建。
func (s *Service) Request(ctx context.Context, runID, toolCallID, action, arguments, reason string) (*model.Approval, error) {
	incidentID, err := s.resolveIncidentID(ctx, runID)
	if err != nil {
		return nil, err
	}

	a := &model.Approval{
		ID:         "ap_" + uuid.NewString(),
		IncidentID: incidentID,
		RunID:      runID,
		ToolCallID: toolCallID,
		Action:     action,
		Arguments:  arguments,
		Reason:     reason,
		Status:     model.ApprovalPending,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := s.repo.Create(ctx, a); err != nil {
		return nil, err
	}
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalPending)).Inc()

	if err := s.moveIncident(ctx, a, model.StatusWaitingApproval); err != nil {
		// 状态闭环迁移失败：审批不能停留在 PENDING（否则会留下无法执行的待审批），
		// 立即置为 EXPIRED 并把原因返回给调用方。
		if expireErr := s.expire(ctx, a, model.ApprovalPending, err); !errors.Is(expireErr, ErrExpired) {
			return nil, expireErr
		}
		return nil, err
	}
	s.publish(a.RunID, "approval.required", map[string]any{"approval_id": a.ID, "action": a.Action})
	return a, nil
}

// Approve 批准并执行动作。
//
// 执行权通过 CAS 获得：只有把 PENDING 原子改成 APPROVED 成功的那一个请求会继续，
// 重复批准 / 并发批准 / 已拒绝 / 已过期的请求返回 ErrNotPending，不会调用 executor。
// 状态链：PENDING -> APPROVED -> EXECUTING -> EXECUTED / FAILED / UNKNOWN。
func (s *Service) Approve(ctx context.Context, id, approvedBy string) (*model.Approval, error) {
	// 1. CAS 取得执行权（approved_by / approved_at 只在这里写入）。
	a, err := s.repo.TransitionStatus(ctx, id, model.ApprovalPending, model.ApprovalApproved, func(a *model.Approval) {
		now := time.Now()
		a.ApprovedBy = approvedBy
		a.ApprovedAt = &now
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, fmt.Errorf("%w: %s", ErrNotPending, id)
		}
		return nil, err
	}
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalApproved)).Inc()
	s.publish(a.RunID, "action.approved", map[string]any{"approval_id": a.ID, "action": a.Action})

	// 2. WAITING_APPROVAL -> MITIGATING；迁移失败说明不再允许执行 -> EXPIRED，且不调用 executor。
	//
	// 注意：这里可能正是因为客户端取消而迁移失败（claim 已成功、ctx 已 canceled）。
	// expire 会用不可取消的持久化 context 落库，否则审批会永久停在 APPROVED。
	if err := s.moveIncident(ctx, a, model.StatusMitigating); err != nil {
		return nil, s.expire(ctx, a, model.ApprovalApproved, err)
	}

	if s.executor == nil {
		// 无执行器（未装配处置通道）：无实际执行，状态停留在 APPROVED。
		return a, nil
	}

	// 3. APPROVED -> EXECUTING 并执行。
	return s.executeClaimed(ctx, a)
}

// executeClaimed 执行已取得执行权的审批（APPROVED -> 终态）。
//
// 关键点：
//   - 状态落库用 persistContext（不可取消 + 短超时）：客户端取消/请求超时不应阻止
//     "外部动作已经发生"这个事实落库，但也不能无限挂起；
//   - 只有执行器明确返回 ErrResultUnknown 才记为 UNKNOWN；
//   - 结果落库失败时显式返回错误，不隐式重执行外部动作。
func (s *Service) executeClaimed(ctx context.Context, a *model.Approval) (*model.Approval, error) {
	persistCtx, cancel := persistContext(ctx)
	next, err := s.repo.TransitionStatus(persistCtx, a.ID, model.ApprovalApproved, model.ApprovalExecuting, nil)
	cancel()
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, fmt.Errorf("%w: %s", ErrNotPending, a.ID)
		}
		return nil, err
	}
	a = next
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalExecuting)).Inc()
	s.publish(a.RunID, "action.executing", map[string]any{"approval_id": a.ID, "action": a.Action})

	// approval_id 作为幂等键通过 context 传给 executor。
	result, execErr := s.executor.Execute(WithApprovalID(ctx, a.ID), a.Action, a.Arguments, a.RunID)

	// 成功 -> EXECUTED；结果未知（超时/取消）-> UNKNOWN；明确失败 -> FAILED。
	status := model.ApprovalExecuted
	switch {
	case execErr == nil:
	case errors.Is(execErr, ErrResultUnknown):
		status = model.ApprovalUnknown
	default:
		status = model.ApprovalFailed
	}

	// 外部动作完成后才开始最终写入的超时，不能让执行耗时消耗落库窗口。
	persistCtx, cancel = persistContext(ctx)
	done, err := s.repo.TransitionStatus(persistCtx, a.ID, model.ApprovalExecuting, status, func(a *model.Approval) {
		if execErr != nil {
			a.Error = execErr.Error()
			return
		}
		a.Result = result.Output
		a.OperationID = result.OperationID
	})
	cancel()
	if err != nil {
		// 执行结果无法落库必须显式报错（此刻外部动作可能已经发生）；
		// 状态停在 EXECUTING，需要运维人工核对外部系统与审批记录。
		return nil, fmt.Errorf("persist approval %s result as %s: %w", a.ID, status, err)
	}
	metrics.ApprovalsTotal.WithLabelValues(done.Action, string(status)).Inc()
	payload := map[string]any{"approval_id": done.ID, "status": string(status)}
	if execErr != nil {
		payload["error"] = execErr.Error()
	} else {
		payload["result"] = result.Output
	}
	s.publish(done.RunID, "action.completed", payload)
	return done, nil
}

// expire 把已取得执行权但无法继续的审批置为 EXPIRED，并返回 ErrExpired 包裹的原始原因。
func (s *Service) expire(ctx context.Context, a *model.Approval, from model.ApprovalStatus, cause error) error {
	persistCtx, cancel := persistContext(ctx)
	defer cancel()
	if _, err := s.repo.TransitionStatus(persistCtx, a.ID, from, model.ApprovalExpired, func(a *model.Approval) {
		a.Error = cause.Error()
	}); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			// 状态已被其他请求改写：不宣称"已过期"，但把原因照实返回。
			return fmt.Errorf("%w: %v", ErrExpired, cause)
		}
		return fmt.Errorf("expire approval %s after %v: %w", a.ID, cause, err)
	}
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalExpired)).Inc()
	s.publish(a.RunID, "action.expired", map[string]any{"approval_id": a.ID, "action": a.Action, "error": cause.Error()})
	return fmt.Errorf("%w: %v", ErrExpired, cause)
}

// ExpirePending 使某个 Incident 下所有 PENDING 审批失效（外部恢复路径使用）。
// 返回过期的审批条数。必须在关闭 Incident 之前调用，保证旧审批不会在恢复后被执行。
func (s *Service) ExpirePending(ctx context.Context, incidentID string) (int, error) {
	if incidentID == "" {
		return 0, fmt.Errorf("%w: incident_id is required", ErrInvalidRequest)
	}
	return s.repo.ExpirePendingByIncident(ctx, incidentID)
}

// Reject 拒绝审批：CAS PENDING -> REJECTED，冲突（重复拒绝/已批准/已过期）返回 ErrNotPending。
func (s *Service) Reject(ctx context.Context, id, rejectedBy string) (*model.Approval, error) {
	a, err := s.repo.TransitionStatus(ctx, id, model.ApprovalPending, model.ApprovalRejected, func(a *model.Approval) {
		now := time.Now()
		a.ApprovedBy = rejectedBy
		a.ApprovedAt = &now
	})
	if err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, fmt.Errorf("%w: %s", ErrNotPending, id)
		}
		return nil, err
	}
	metrics.ApprovalsTotal.WithLabelValues(a.Action, string(model.ApprovalRejected)).Inc()
	if err := s.moveIncident(ctx, a, model.StatusFailed); err != nil {
		return nil, err
	}
	s.publish(a.RunID, "action.rejected", map[string]any{"approval_id": a.ID, "action": a.Action})
	return a, nil
}

// Get 查询审批。
func (s *Service) Get(ctx context.Context, id string) (*model.Approval, error) {
	return s.repo.Get(ctx, id)
}

// ListByRun 返回某个 Run 的审批（按创建时间正序）。查询 API 用。
func (s *Service) ListByRun(ctx context.Context, runID string) ([]*model.Approval, error) {
	return s.repo.ListByRun(ctx, runID)
}

// ListPending 返回所有 PENDING 审批（按创建时间正序）。查询 API 用。
func (s *Service) ListPending(ctx context.Context) ([]*model.Approval, error) {
	return s.repo.ListPending(ctx)
}

// GetRun 查询审批所属 Run，供查询 API 做父资源存在性校验（未装配 Run 仓库时返回 ErrNotFound）。
func (s *Service) GetRun(ctx context.Context, runID string) (*model.AgentRun, error) {
	if s.runRepo == nil {
		return nil, ErrNotFound
	}
	return s.runRepo.GetRun(ctx, runID)
}

func (s *Service) publish(runID, typ string, data map[string]any) {
	if s.broker != nil {
		s.broker.Publish(runID, agentruntime.StreamEvent{Type: typ, Data: data})
	}
}
