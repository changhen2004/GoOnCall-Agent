package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"gooncall-agent/internal/agent/core"
	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/tools"
)

// Model 是一个可被调用方显式选择的模型连接。
type Model struct {
	Provider        llm.Provider
	ContextWindow   int
	MaxOutputTokens int
}

// Options 是会话服务配置。
type Options struct {
	Models           map[string]Model
	DefaultModel     string
	MaxRounds        int
	RunBudget        time.Duration
	ApprovalTTL      time.Duration
	SystemPrompt     string
	Registry         *tools.Registry
	ProcessID        string
	Logger           *slog.Logger
	SubscriberBuffer int
	PersistTimeout   time.Duration
}

// Service 管理会话、运行生命周期、审批与事件分发。
type Service struct {
	store  Store
	models map[string]Model
	opts   Options
	logger *slog.Logger

	mu      sync.Mutex
	active  map[string]*activeRun
	subs    map[string]map[*subscriber]struct{}
	waiters map[string]*approvalWaiter
	wg      sync.WaitGroup
	closed  bool
}

type activeRun struct {
	cancel context.CancelFunc
}

// RunDetail 是一次运行的完整查询结果。
type RunDetail struct {
	Run       *Run            `json:"run"`
	Tools     []ToolExecution `json:"tool_executions"`
	Approvals []Approval      `json:"pending_approvals"`
}

// New 创建会话服务。
func New(store Store, opts Options) (*Service, error) {
	if store == nil {
		return nil, errors.New("store 不能为空")
	}
	if len(opts.Models) == 0 {
		return nil, errors.New("至少需要一个模型连接")
	}
	if opts.DefaultModel == "" && len(opts.Models) == 1 {
		for name := range opts.Models {
			opts.DefaultModel = name
		}
	}
	if _, ok := opts.Models[opts.DefaultModel]; !ok {
		return nil, fmt.Errorf("默认模型连接 %q 不存在", opts.DefaultModel)
	}
	if opts.Registry == nil {
		return nil, errors.New("工具注册表不能为空")
	}

	if opts.MaxRounds <= 0 {
		opts.MaxRounds = 8
	}
	if opts.RunBudget <= 0 {
		opts.RunBudget = 3 * time.Minute
	}
	if opts.ApprovalTTL <= 0 {
		opts.ApprovalTTL = 5 * time.Minute
	}
	if opts.SystemPrompt == "" {
		opts.SystemPrompt = "你是一个可靠的助手，需要时使用提供的工具完成任务。"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = 64
	}
	if opts.PersistTimeout <= 0 {
		opts.PersistTimeout = 5 * time.Second
	}

	return &Service{
		store:   store,
		models:  opts.Models,
		opts:    opts,
		logger:  opts.Logger,
		active:  make(map[string]*activeRun),
		subs:    make(map[string]map[*subscriber]struct{}),
		waiters: make(map[string]*approvalWaiter),
	}, nil
}

// CreateConversation 为当前操作者创建会话。
func (s *Service) CreateConversation(ctx context.Context, owner string) (*Conversation, error) {
	if owner == "" {
		return nil, ErrInvalid
	}
	conversation := Conversation{
		ID: uuid.NewString(), Owner: owner, OwnerKind: OwnerKindUser, CreatedAt: time.Now(),
	}
	if err := s.store.CreateConversation(ctx, conversation); err != nil {
		return nil, err
	}
	return &conversation, nil
}

// Conversation 查询会话、消息和运行记录。
func (s *Service) Conversation(ctx context.Context, actor, conversationID string) (*Conversation, []Message, []Run, error) {
	conversation, err := s.owned(ctx, actor, conversationID)
	if err != nil {
		return nil, nil, nil, err
	}
	messages, err := s.store.Messages(ctx, conversationID)
	if err != nil {
		return nil, nil, nil, err
	}
	runs, err := s.store.Runs(ctx, conversationID)
	if err != nil {
		return nil, nil, nil, err
	}
	return conversation, messages, runs, nil
}

// Submit 在用户消息和运行成功保存后返回；执行在后台继续，独立于本次请求连接。
func (s *Service) Submit(ctx context.Context, actor, conversationID, content, modelName string) (*Run, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrInvalid
	}
	if _, err := s.owned(ctx, actor, conversationID); err != nil {
		return nil, err
	}

	if modelName == "" {
		modelName = s.opts.DefaultModel
	}
	model, ok := s.models[modelName]
	if !ok {
		return nil, fmt.Errorf("%w: 未知模型连接 %q", ErrInvalid, modelName)
	}

	now := time.Now()
	run := Run{
		ID:             uuid.NewString(),
		ConversationID: conversationID,
		Model:          modelName,
		Status:         RunRunning,
		BudgetMS:       s.opts.RunBudget.Milliseconds(),
		ProcessID:      s.opts.ProcessID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	userMessage := Message{
		ConversationID: conversationID,
		RunID:          run.ID,
		Role:           llm.RoleUser,
		Content:        content,
		Status:         MessageComplete,
		CreatedAt:      now,
	}

	// 先登记运行上下文与 WaitGroup，避免 StartRun/关闭之间的竞态。
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return nil, ErrConflict
	}
	s.wg.Add(1)
	s.active[run.ID] = &activeRun{cancel: cancel}
	s.mu.Unlock()

	if err := s.store.StartRun(ctx, run, userMessage); err != nil {
		s.wg.Done()
		s.removeActive(run.ID)
		return nil, err
	}

	go func() {
		defer s.wg.Done()
		s.execute(runCtx, run, content, model)
	}()
	return &run, nil
}

// RunDetail 查询运行、工具执行与待审批项。
func (s *Service) RunDetail(ctx context.Context, actor, runID string) (*RunDetail, error) {
	run, err := s.store.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	if _, err := s.owned(ctx, actor, run.ConversationID); err != nil {
		return nil, err
	}
	executions, err := s.store.ToolExecutions(ctx, runID)
	if err != nil {
		return nil, err
	}
	approvals, err := s.store.PendingApprovals(ctx, runID)
	if err != nil {
		return nil, err
	}
	return &RunDetail{Run: run, Tools: executions, Approvals: approvals}, nil
}

// Cancel 主动取消运行。已经开始的工具副作用不会回滚。
func (s *Service) Cancel(ctx context.Context, actor, runID string) error {
	run, err := s.store.Run(ctx, runID)
	if err != nil {
		return err
	}
	if _, err := s.owned(ctx, actor, run.ConversationID); err != nil {
		return err
	}

	updated, err := s.store.UpdateRunStatus(ctx, runID, RunCanceled, "用户主动取消", ActiveRunStatuses()...)
	if err != nil {
		return err
	}
	if !updated {
		return ErrConflict
	}
	if err := s.store.InvalidateApprovals(ctx, runID); err != nil {
		s.logger.Error("失效待审批项失败", "run_id", runID, "error", err)
	}

	s.mu.Lock()
	active := s.active[runID]
	s.mu.Unlock()
	if active != nil {
		active.cancel()
	}

	s.emit(runID, Event{Type: "run_status", Status: string(RunCanceled), Error: "用户主动取消"})
	return nil
}

// CancelOwnerRuns 取消指定用户的全部活动运行，用于禁用账号。
//
// 由运行中服务的禁用检测循环调用；已被取消或已结束的运行不会重复处理。
func (s *Service) CancelOwnerRuns(ctx context.Context, ownerID, reason string) (int, error) {
	runs, err := s.store.ActiveRunsByOwner(ctx, ownerID)
	if err != nil {
		return 0, err
	}

	canceled := 0
	for _, run := range runs {
		updated, err := s.store.UpdateRunStatus(ctx, run.ID, RunCanceled, reason, ActiveRunStatuses()...)
		if err != nil {
			return canceled, err
		}
		if !updated {
			continue
		}
		if err := s.store.InvalidateApprovals(ctx, run.ID); err != nil {
			s.logger.Error("失效待审批项失败", "run_id", run.ID, "error", err)
		}

		s.mu.Lock()
		active := s.active[run.ID]
		s.mu.Unlock()
		if active != nil {
			active.cancel()
		}
		s.emit(run.ID, Event{Type: "run_status", Status: string(RunCanceled), Error: reason})
		canceled++
	}
	return canceled, nil
}

// Shutdown 取消所有运行并等待其保存终态。
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for _, active := range s.active {
		active.cancel()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) execute(ctx context.Context, run Run, user string, model Model) {
	defer s.removeActive(run.ID)

	persistCtx, cancel := s.persistContext()
	history, err := s.store.CompletedHistory(persistCtx, run.ConversationID, run.ID)
	cancel()
	if err != nil {
		s.endRun(run.ID, RunFailed, "加载会话历史失败: "+err.Error())
		return
	}

	hooks := newRunHooks(s, run)
	runner := core.New(model.Provider, s.opts.Registry, hooks, s.emitter(run.ID))
	result, runErr := runner.Run(ctx, core.RunConfig{
		SystemPrompt:    s.opts.SystemPrompt,
		History:         groupTurns(history),
		User:            user,
		MaxRounds:       s.opts.MaxRounds,
		MaxOutputTokens: model.MaxOutputTokens,
		ContextWindow:   model.ContextWindow,
		Budget:          s.opts.RunBudget,
	})

	status, reason := RunFailed, ""
	if result != nil {
		status = mapStatus(result.Status)
		reason = result.Reason
	}
	if runErr != nil && reason == "" {
		reason = runErr.Error()
	}
	s.endRun(run.ID, status, reason)
}

func (s *Service) endRun(runID string, status RunStatus, reason string) {
	ctx, cancel := s.persistContext()
	defer cancel()

	updated, err := s.store.UpdateRunStatus(ctx, runID, status, reason, ActiveRunStatuses()...)
	if err != nil {
		s.logger.Error("保存运行终态失败", "run_id", runID, "error", err)
	}
	if !updated {
		// 取消等操作可能已经写入终态，以数据库为准。
		if current, getErr := s.store.Run(ctx, runID); getErr == nil {
			status, reason = current.Status, current.Reason
		}
	}
	if err := s.store.InvalidateApprovals(ctx, runID); err != nil {
		s.logger.Error("失效待审批项失败", "run_id", runID, "error", err)
	}

	s.emit(runID, Event{Type: "run_finished", Status: string(status), Error: reason})
	s.closeSubscribers(runID)
}

func (s *Service) removeActive(runID string) {
	s.mu.Lock()
	active := s.active[runID]
	delete(s.active, runID)
	s.mu.Unlock()
	if active != nil {
		active.cancel()
	}
}

func (s *Service) emitter(runID string) func(core.Event) {
	return func(event core.Event) {
		s.emit(runID, Event{
			Type:       string(event.Type),
			Delta:      event.Delta,
			ToolCallID: event.ToolCallID,
			ToolName:   event.ToolName,
			Arguments:  event.Arguments,
			ApprovalID: event.ApprovalID,
			Status:     event.Status,
			Error:      event.Error,
		})
	}
}

func (s *Service) persistContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.opts.PersistTimeout)
}

func (s *Service) owned(ctx context.Context, actor, conversationID string) (*Conversation, error) {
	conversation, err := s.store.Conversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conversation.OwnerKind != OwnerKindUser || conversation.Owner != actor {
		return nil, ErrForbidden
	}
	return conversation, nil
}

func groupTurns(messages []Message) []core.Turn {
	turns := make([]core.Turn, 0, len(messages))
	currentRun := ""
	for _, message := range messages {
		if message.RunID != currentRun {
			turns = append(turns, core.Turn{})
			currentRun = message.RunID
		}
		turn := &turns[len(turns)-1]
		turn.Messages = append(turn.Messages, message.LLM())
	}
	return turns
}

func mapStatus(status core.Status) RunStatus {
	switch status {
	case core.StatusCompleted:
		return RunCompleted
	case core.StatusCanceled:
		return RunCanceled
	case core.StatusTimeout:
		return RunTimeout
	case core.StatusUnknown:
		return RunUnknown
	default:
		return RunFailed
	}
}
