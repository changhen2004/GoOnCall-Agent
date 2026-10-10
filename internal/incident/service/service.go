// Package service 实现 Incident 的业务逻辑：创建去重、查询、状态流转。
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/incident/state_machine"
	"gooncall-agent/internal/observability/metrics"
)

var (
	// ErrInvalidInput 表示创建参数非法。
	ErrInvalidInput = errors.New("invalid incident input")
	// ErrNotFound 复用 repository 的未找到错误。
	ErrNotFound = repository.ErrNotFound
	// ErrInvalidTransition 表示非法状态迁移。
	ErrInvalidTransition = errors.New("invalid incident transition")
	// ErrConcurrentModification 表示并发状态迁移冲突（乐观锁 version CAS 失败）。
	ErrConcurrentModification = repository.ErrConcurrentModification
)

// Service 是 Incident 业务服务。
type Service struct {
	repo            repository.Repository
	approvalExpirer PendingApprovalExpirer
}

// PendingApprovalExpirer 让某个 Incident 的所有 PENDING 审批失效。
//
// 由 Bootstrap 注入 approval.Service 实现（approval 依赖本包，因此这里只能是接口）。
type PendingApprovalExpirer interface {
	ExpirePending(ctx context.Context, incidentID string) (int, error)
}

// New 创建一个 Incident Service。
//
// Incident 状态写入与审计事件写入由 Repository 在同一个事务/临界区内完成，
// 因此这里不需要再单独装配事件仓库。
func New(repo repository.Repository) *Service {
	return &Service{repo: repo}
}

// WithApprovalExpirer 注入"外部恢复前先让待审批失效"的组件。
func (s *Service) WithApprovalExpirer(e PendingApprovalExpirer) *Service {
	s.approvalExpirer = e
	return s
}

// CreateIncidentInput 是创建 Incident 的入参。
type CreateIncidentInput struct {
	Service     string
	Severity    string
	Title       string
	Description string
	AlertName   string
}

// Create 创建一个 Incident，并基于指纹去重。
//
// 去重语义（同一 fingerprint 一次只允许一条非终态 Incident）：
//   - 命中非终态同指纹 Incident：直接返回已有记录（created=false），同一告警周期内去重
//   - 只有终态（RESOLVED/FAILED/CANCELLED）历史：视为告警复发，创建**新的** Incident
//     （新 ID、新周期），不重开历史记录
//
// 返回创建后的 Incident 以及是否为新创建。
func (s *Service) Create(ctx context.Context, in CreateIncidentInput) (*model.Incident, bool, error) {
	if in.Service == "" {
		return nil, false, fmt.Errorf("%w: service is required", ErrInvalidInput)
	}
	if in.Title == "" {
		return nil, false, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}

	fingerprint := model.Fingerprint(in.Service, in.AlertName, in.Title)

	// 去重：只命中活动（非终态）Incident。
	if existing, err := s.repo.GetActiveByFingerprint(ctx, fingerprint); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, false, err
	}

	now := time.Now()
	inc := &model.Incident{
		ID:          "inc_" + uuid.NewString(),
		Service:     in.Service,
		Severity:    in.Severity,
		Title:       in.Title,
		Description: in.Description,
		AlertName:   in.AlertName,
		Fingerprint: fingerprint,
		Status:      model.StatusOpen,
		StartedAt:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if inc.Severity == "" {
		inc.Severity = string(model.SeverityMedium)
	}

	// 与审计事件同事务写入：事件写失败则 Incident 不落库，接口报错后重试不会留下半截状态。
	ev := model.NewIncidentEvent(inc.ID, model.EventIncidentCreated, "", model.StatusOpen, "alert firing", "")
	if err := s.repo.CreateWithEvent(ctx, inc, ev); err != nil {
		// 并发去重兜底：部分唯一索引冲突说明已有人创建了活动 Incident，
		// 返回已有记录（created=false），避免高并发下同一告警周期出现两条。
		if errors.Is(err, repository.ErrConflict) {
			if existing, gerr := s.repo.GetActiveByFingerprint(ctx, fingerprint); gerr == nil {
				return existing, false, nil
			}
		}
		return nil, false, err
	}
	metrics.IncidentsTotal.WithLabelValues(inc.Service, inc.Severity).Inc()
	return inc, true, nil
}

// Get 按 ID 查询 Incident。
func (s *Service) Get(ctx context.Context, id string) (*model.Incident, error) {
	return s.repo.GetByID(ctx, id)
}

// GetByFingerprint 按去重指纹查询 Incident。
func (s *Service) GetByFingerprint(ctx context.Context, fingerprint string) (*model.Incident, error) {
	return s.repo.GetByFingerprint(ctx, fingerprint)
}

// ResolveExternally 由外部告警恢复触发，直接关闭 Incident（绕过 Agent 审批/处置流程）。
//
//   - 处置尚未开始的活动 Incident：关闭它（只关闭最新活动记录）。
//   - MITIGATING / VERIFYING：拒绝关闭，避免外部恢复与已取得执行权的处置互相覆盖。
//   - 只有终态历史：幂等成功，返回最近一条历史记录且不做任何修改
//     （调用方 internal/api/handler/alert.go 会读取返回记录的 Status，不能返回 nil）。
//   - 完全没有该指纹的记录：返回 ErrNotFound（无从恢复，不伪造记录）。
func (s *Service) ResolveExternally(ctx context.Context, fingerprint string) (*model.Incident, error) {
	inc, err := s.repo.GetActiveByFingerprint(ctx, fingerprint)
	if errors.Is(err, repository.ErrNotFound) {
		latest, gerr := s.repo.GetByFingerprint(ctx, fingerprint)
		if errors.Is(gerr, repository.ErrNotFound) {
			return nil, fmt.Errorf("%w: no incident for fingerprint", repository.ErrNotFound)
		}
		if gerr != nil {
			return nil, gerr
		}
		// 历史终态记录：幂等成功，不做修改。
		return latest, nil
	}
	if err != nil {
		return nil, err
	}
	if inc.Status == model.StatusMitigating || inc.Status == model.StatusVerifying {
		return nil, fmt.Errorf("%w: incident %s is %s", ErrInvalidTransition, inc.ID, inc.Status)
	}

	// 外部恢复顺序：先让该 Incident 的 PENDING 审批失效，再关闭 Incident。
	// 反过来的话会留下"Incident 已恢复、审批仍可被批准"的窗口；任一步失败都返回错误，不关单。
	if s.approvalExpirer != nil {
		if _, err := s.approvalExpirer.ExpirePending(ctx, inc.ID); err != nil {
			return nil, fmt.Errorf("expire pending approvals of %s: %w", inc.ID, err)
		}
	}

	from := inc.Status
	now := time.Now()
	inc.Status = model.StatusResolved
	inc.ResolvedAt = &now
	inc.UpdatedAt = now
	// 与审计事件同事务更新。
	ev := model.NewIncidentEvent(inc.ID, model.EventStatusTransition, from, model.StatusResolved, "external alert resolved", "")
	if err := s.repo.UpdateWithEvent(ctx, inc, ev); err != nil {
		return nil, err
	}
	return inc, nil
}

// List 按过滤条件查询 Incident 列表。
func (s *Service) List(ctx context.Context, filter repository.ListFilter) ([]*model.Incident, error) {
	return s.repo.List(ctx, filter)
}

// Transition 执行一次受状态机约束的状态迁移。
func (s *Service) Transition(ctx context.Context, id string, to model.Status) (*model.Incident, error) {
	return s.transition(ctx, id, to, "state machine transition", "")
}

// transition 执行状态迁移并写入事件；非法迁移不写事件。
func (s *Service) transition(ctx context.Context, id string, to model.Status, reason, errMsg string) (*model.Incident, error) {
	return s.transitionWithAudit(ctx, id, to, reason, errMsg, "", nil)
}

// MoveToWithAudit 原子写入状态迁移和一条业务审计事件。
func (s *Service) MoveToWithAudit(ctx context.Context, id string, to model.Status, eventType string, payload map[string]any) (*model.Incident, error) {
	return s.transitionWithAudit(ctx, id, to, "state machine transition", "", eventType, payload)
}

func (s *Service) transitionWithAudit(ctx context.Context, id string, to model.Status, reason, errMsg, eventType string, payload map[string]any) (*model.Incident, error) {
	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	from := inc.Status
	if err := state_machine.Transition(from, to); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}

	inc.Status = to
	inc.UpdatedAt = time.Now()
	if to == model.StatusResolved {
		now := time.Now()
		inc.ResolvedAt = &now
	}

	// 与审计事件同事务更新：事件写失败则状态不落库，调用方可安全重试同一迁移。
	statusEvent := model.NewIncidentEvent(inc.ID, model.EventStatusTransition, from, to, reason, errMsg)
	var updateErr error
	if eventType == "" {
		updateErr = s.repo.UpdateWithEvent(ctx, inc, statusEvent)
	} else {
		auditPayload := make(map[string]any, len(payload)+2)
		for key, value := range payload {
			auditPayload[key] = value
		}
		auditPayload["from"] = string(from)
		auditPayload["to"] = string(to)
		auditEvent := model.NewAuditEvent(inc.ID, eventType, auditPayload)
		updateErr = s.repo.UpdateWithEvents(ctx, inc, []*model.IncidentEvent{statusEvent, auditEvent})
	}
	if updateErr != nil {
		if errors.Is(updateErr, repository.ErrConcurrentModification) {
			return nil, fmt.Errorf("%w: incident %s", ErrConcurrentModification, id)
		}
		return nil, updateErr
	}
	return inc, nil
}

// Fail 将 Incident 置为 FAILED 并把原因写入终态事件。
// 供消费重试耗尽等终态回调使用（OPEN/INVESTIGATING/WAITING_APPROVAL/MITIGATING/VERIFYING -> FAILED）。
func (s *Service) Fail(ctx context.Context, id, reason, errMsg string) (*model.Incident, error) {
	return s.transition(ctx, id, model.StatusFailed, reason, errMsg)
}

// ListEvents 返回某个 Incident 的生命周期事件（时间正序）。父资源不存在返回 ErrNotFound。
func (s *Service) ListEvents(ctx context.Context, incidentID string) ([]*model.IncidentEvent, error) {
	if _, err := s.repo.GetByID(ctx, incidentID); err != nil {
		return nil, err
	}
	return s.repo.ListEvents(ctx, incidentID)
}

// MoveTo 执行一次受严格状态机约束的状态迁移（v1.1 状态闭环）。
func (s *Service) MoveTo(ctx context.Context, id string, next model.Status) (*model.Incident, error) {
	return s.Transition(ctx, id, next)
}

// Analyze 将 Incident 置为 INVESTIGATING（OPEN -> INVESTIGATING）。
func (s *Service) Analyze(ctx context.Context, id string) (*model.Incident, error) {
	return s.Transition(ctx, id, model.StatusInvestigating)
}

// Resolve 将 Incident 置为 RESOLVED（仅 VERIFYING -> RESOLVED）。
func (s *Service) Resolve(ctx context.Context, id string) (*model.Incident, error) {
	return s.Transition(ctx, id, model.StatusResolved)
}
