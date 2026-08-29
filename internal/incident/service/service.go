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
)

// Service 是 Incident 业务服务。
type Service struct {
	repo repository.Repository
}

// New 创建一个 Incident Service。
func New(repo repository.Repository) *Service {
	return &Service{repo: repo}
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
// 返回创建后的 Incident 以及是否为新创建（false 表示命中去重、返回已有记录）。
func (s *Service) Create(ctx context.Context, in CreateIncidentInput) (*model.Incident, bool, error) {
	if in.Service == "" {
		return nil, false, fmt.Errorf("%w: service is required", ErrInvalidInput)
	}
	if in.Title == "" {
		return nil, false, fmt.Errorf("%w: title is required", ErrInvalidInput)
	}

	fingerprint := model.Fingerprint(in.Service, in.AlertName, in.Title)

	// 去重：同服务同告警（或同标题）视为同一次故障。
	if existing, err := s.repo.GetByFingerprint(ctx, fingerprint); err == nil {
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

	if err := s.repo.Create(ctx, inc); err != nil {
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

// ResolveByFingerprint 按指纹关闭 Incident（OPEN 先转 INVESTIGATING 再关闭）。
func (s *Service) ResolveByFingerprint(ctx context.Context, fingerprint string) (*model.Incident, error) {
	inc, err := s.repo.GetByFingerprint(ctx, fingerprint)
	if err != nil {
		return nil, err
	}
	if inc.Status == model.StatusOpen {
		if inc, err = s.Analyze(ctx, inc.ID); err != nil {
			return nil, err
		}
	}
	if inc.Status.IsTerminal() {
		return inc, nil
	}
	return s.Resolve(ctx, inc.ID)
}

// List 按过滤条件查询 Incident 列表。
func (s *Service) List(ctx context.Context, filter repository.ListFilter) ([]*model.Incident, error) {
	return s.repo.List(ctx, filter)
}

// Transition 执行一次受状态机约束的状态迁移。
func (s *Service) Transition(ctx context.Context, id string, to model.Status) (*model.Incident, error) {
	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := state_machine.Transition(inc.Status, to); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTransition, err)
	}

	inc.Status = to
	inc.UpdatedAt = time.Now()
	if to == model.StatusResolved {
		now := time.Now()
		inc.ResolvedAt = &now
	}

	if err := s.repo.Update(ctx, inc); err != nil {
		return nil, err
	}
	return inc, nil
}

// Analyze 将 Incident 置为 INVESTIGATING（OPEN -> INVESTIGATING）。
func (s *Service) Analyze(ctx context.Context, id string) (*model.Incident, error) {
	return s.Transition(ctx, id, model.StatusInvestigating)
}

// Resolve 将 Incident 置为 RESOLVED（INVESTIGATING/VERIFYING -> RESOLVED）。
func (s *Service) Resolve(ctx context.Context, id string) (*model.Incident, error) {
	return s.Transition(ctx, id, model.StatusResolved)
}
