// Package core 实现单次运行的模型/工具循环。
//
// 核心不访问数据库，也不保存会话：完整消息和工具状态通过 Hooks 同步交给会话层，
// 展示事件通过 Emit 交给有界分发器。
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/tools"
)

// Status 是一次运行的最终状态。
type Status string

const (
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusTimeout   Status = "timeout"
	StatusMaxRounds Status = "max_rounds"
	StatusUnknown   Status = "unknown"
)

// ToolStatus 是工具执行结果状态。
type ToolStatus string

const (
	ToolSucceeded ToolStatus = "succeeded"
	ToolFailed    ToolStatus = "failed"
	ToolRejected  ToolStatus = "rejected"
	ToolCanceled  ToolStatus = "canceled"
	ToolUnknown   ToolStatus = "unknown"
)

var (
	// ErrToolRejected 表示用户拒绝了需要审批的工具调用。
	ErrToolRejected = errors.New("工具调用被用户拒绝")
	// ErrApprovalExpired 表示审批在有效期内未被批准。
	ErrApprovalExpired = errors.New("审批已过期")
	// ErrRunSuperseded 表示运行已被取消或在重启后失效。
	ErrRunSuperseded = errors.New("运行已失效")
)

// Hooks 是核心与会话层的同步契约。
//
// 所有保存方法返回 nil 后才能继续后续模型或工具调用；返回错误会立即停止运行。
type Hooks interface {
	// SaveMessages 同步保存完整的新消息。
	SaveMessages(ctx context.Context, messages []llm.Message) error
	// SaveDraft 保存未完成的流式文本草稿。
	SaveDraft(ctx context.Context, content string) error
	// BeginTool 保存工具执行记录；需要审批时阻塞等待并取得执行权。
	// 返回 ErrToolRejected 表示用户拒绝，调用方应以 ToolRejected 调用 EndTool。
	BeginTool(ctx context.Context, call llm.ToolCall, tool tools.Tool, args json.RawMessage) error
	// EndTool 在同一事务中保存工具执行结果与对应工具消息。
	EndTool(ctx context.Context, call llm.ToolCall, status ToolStatus, content string) error
}

// RunConfig 是一次运行所需的全部输入。
type RunConfig struct {
	SystemPrompt    string
	History         []Turn
	User            string
	MaxRounds       int
	MaxOutputTokens int
	ContextWindow   int
	Budget          time.Duration
}

// Result 是一次运行的输出。
type Result struct {
	Status   Status
	Reason   string
	Content  string
	Messages []llm.Message
}

// Runner 执行一次输入的完整模型/工具循环。
type Runner struct {
	provider llm.Provider
	registry *tools.Registry
	hooks    Hooks
	emit     func(Event)
}

// New 创建 Runner。emit 可以为 nil。
func New(provider llm.Provider, registry *tools.Registry, hooks Hooks, emit func(Event)) *Runner {
	return &Runner{provider: provider, registry: registry, hooks: hooks, emit: emit}
}

// Run 执行循环，直到模型正常完成、取消、超时或达到轮数上限。
func (r *Runner) Run(ctx context.Context, cfg RunConfig) (*Result, error) {
	cfg = withDefaults(cfg)
	budget := newRunBudget(cfg.Budget)
	defer budget.pause()

	toolDefs := r.registry.Definitions()
	current := []llm.Message{{Role: llm.RoleUser, Content: cfg.User}}
	produced := make([]llm.Message, 0, 8)

	for round := 1; round <= cfg.MaxRounds; round++ {
		if ctx.Err() != nil {
			return r.finish(StatusCanceled, "运行已取消", produced, "")
		}
		if budget.expired() {
			return r.finish(StatusTimeout, "执行超时", produced, "")
		}

		input, err := buildInput(cfg.SystemPrompt, toolDefs, cfg.History, current, cfg.ContextWindow, cfg.MaxOutputTokens)
		if err != nil {
			return r.finish(StatusFailed, err.Error(), produced, "")
		}

		resp, partial, err := r.callModel(ctx, budget, llm.Request{
			Messages:  input,
			Tools:     toolDefs,
			MaxTokens: cfg.MaxOutputTokens,
		})
		if err != nil {
			status, reason := classify(ctx, budget, err)
			r.saveDraft(partial)
			return r.finish(status, reason, produced, partial)
		}
		if resp.FinishReason == "length" {
			r.saveDraft(resp.Content)
			return r.finish(StatusFailed, "模型输出因长度限制被截断", produced, resp.Content)
		}
		if resp.FinishReason == "content_filter" {
			r.saveDraft(resp.Content)
			return r.finish(StatusFailed, "模型输出被内容策略截断", produced, resp.Content)
		}

		assistant := llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		if err := r.hooks.SaveMessages(ctx, []llm.Message{assistant}); err != nil {
			return r.finish(StatusFailed, "保存模型消息失败: "+err.Error(), produced, resp.Content)
		}
		produced = append(produced, assistant)
		current = append(current, assistant)

		if len(resp.ToolCalls) == 0 {
			if resp.FinishReason == "tool_calls" || resp.FinishReason == "function_call" {
				return r.finish(StatusFailed, "模型声明了工具调用但没有返回完整调用", produced, resp.Content)
			}
			return &Result{
				Status:   StatusCompleted,
				Content:  resp.Content,
				Messages: produced,
			}, nil
		}

		for _, call := range resp.ToolCalls {
			outcome := r.executeTool(ctx, budget, call)
			if outcome.message.Role != "" {
				produced = append(produced, outcome.message)
				current = append(current, outcome.message)
			}
			if outcome.abort != "" {
				return r.finish(outcome.abort, outcome.reason, produced, resp.Content)
			}
		}
	}

	return r.finish(StatusMaxRounds, fmt.Sprintf("达到最大模型调用轮数 %d", cfg.MaxRounds), produced, "")
}

type toolOutcome struct {
	message llm.Message
	abort   Status
	reason  string
}

func (r *Runner) executeTool(ctx context.Context, budget *runBudget, call llm.ToolCall) toolOutcome {
	tool, ok := r.registry.Get(call.Function.Name)
	if !ok {
		return r.backfill(ctx, call, "工具不存在: "+call.Function.Name)
	}

	args := json.RawMessage(call.Function.Arguments)
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var probe map[string]any
	if err := json.Unmarshal(args, &probe); err != nil || probe == nil {
		return r.backfill(ctx, call, "工具参数不是有效的 JSON 对象")
	}

	if tool.RequiresApproval {
		budget.pause()
	}
	beginErr := r.hooks.BeginTool(ctx, call, tool, args)
	if tool.RequiresApproval {
		budget.resume()
	}
	if beginErr != nil {
		if errors.Is(beginErr, ErrToolRejected) {
			content := "用户拒绝了该操作，工具未执行。"
			if err := r.hooks.EndTool(ctx, call, ToolRejected, content); err != nil {
				return toolOutcome{abort: StatusFailed, reason: "保存工具结果失败: " + err.Error()}
			}
			return toolOutcome{message: toolMessage(call, content)}
		}
		status, reason := classify(ctx, budget, beginErr)
		return toolOutcome{abort: status, reason: reason}
	}

	toolCtx, cancel := budget.context(ctx)
	result, runErr := r.registry.Execute(toolCtx, tool.Name, args)
	toolCtxErr := toolCtx.Err()
	cancel()

	switch {
	case runErr == nil:
		if err := r.hooks.EndTool(ctx, call, ToolSucceeded, result); err != nil {
			return toolOutcome{abort: StatusFailed, reason: "保存工具结果失败: " + err.Error()}
		}
		return toolOutcome{message: toolMessage(call, result)}

	case tools.IsUnknown(runErr):
		content := "工具执行结果未知: " + runErr.Error()
		if err := r.hooks.EndTool(context.Background(), call, ToolUnknown, content); err != nil {
			return toolOutcome{abort: StatusFailed, reason: "保存工具结果失败: " + err.Error()}
		}
		return toolOutcome{abort: StatusUnknown, reason: runErr.Error()}

	case toolCtxErr != nil:
		status := StatusTimeout
		reason := "执行超时"
		if ctx.Err() != nil {
			status = StatusCanceled
			reason = "运行已取消"
		}
		execStatus := ToolCanceled
		if tool.RequiresApproval {
			execStatus = ToolUnknown
			reason += "，外部操作结果未知"
		}
		content := "工具执行未正常完成: " + runErr.Error()
		_ = r.hooks.EndTool(context.Background(), call, execStatus, content)
		return toolOutcome{abort: status, reason: reason}

	default:
		content := "工具执行失败: " + runErr.Error()
		if err := r.hooks.EndTool(ctx, call, ToolFailed, content); err != nil {
			return toolOutcome{abort: StatusFailed, reason: "保存工具结果失败: " + err.Error()}
		}
		return toolOutcome{message: toolMessage(call, content)}
	}
}

// backfill 保存一条未实际执行的工具调用错误消息（未知工具、非法参数）。
func (r *Runner) backfill(ctx context.Context, call llm.ToolCall, content string) toolOutcome {
	msg := toolMessage(call, content)
	if err := r.hooks.SaveMessages(ctx, []llm.Message{msg}); err != nil {
		return toolOutcome{abort: StatusFailed, reason: "保存工具结果失败: " + err.Error()}
	}
	return toolOutcome{message: msg}
}

func toolMessage(call llm.ToolCall, content string) llm.Message {
	return llm.Message{
		Role:       llm.RoleTool,
		Content:    content,
		ToolCallID: call.ID,
		Name:       call.Function.Name,
	}
}

func (r *Runner) callModel(ctx context.Context, budget *runBudget, req llm.Request) (*llm.Response, string, error) {
	callCtx, cancel := budget.context(ctx)
	defer cancel()

	stream, err := r.provider.ChatStream(callCtx, req)
	if err != nil {
		return nil, "", err
	}
	defer stream.Close()

	var partial strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, partial.String(), err
		}
		if chunk.Content != "" {
			partial.WriteString(chunk.Content)
			r.emitEvent(Event{Type: EventTextDelta, Delta: chunk.Content})
		}
	}

	resp, err := stream.Response()
	if err != nil {
		return nil, partial.String(), err
	}
	if resp.Content == "" {
		resp.Content = partial.String()
	}
	return resp, partial.String(), nil
}

func (r *Runner) saveDraft(content string) {
	if strings.TrimSpace(content) == "" {
		return
	}
	_ = r.hooks.SaveDraft(context.Background(), content)
}

func (r *Runner) emitEvent(ev Event) {
	if r.emit != nil {
		r.emit(ev)
	}
}

func (r *Runner) finish(status Status, reason string, produced []llm.Message, content string) (*Result, error) {
	result := &Result{Status: status, Reason: reason, Content: content, Messages: produced}
	if status == StatusCompleted {
		return result, nil
	}
	if reason == "" {
		reason = string(status)
		result.Reason = reason
	}
	return result, errors.New(reason)
}

func classify(ctx context.Context, budget *runBudget, err error) (Status, string) {
	switch {
	case errors.Is(err, ErrApprovalExpired):
		return StatusFailed, "审批已过期，运行终止"
	case errors.Is(err, ErrRunSuperseded):
		return StatusCanceled, "运行已失效"
	case errors.Is(err, context.Canceled) || ctx.Err() != nil:
		return StatusCanceled, "运行已取消"
	case errors.Is(err, context.DeadlineExceeded) || budget.expired():
		return StatusTimeout, "执行超时"
	default:
		return StatusFailed, err.Error()
	}
}

func withDefaults(cfg RunConfig) RunConfig {
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 8
	}
	if cfg.MaxOutputTokens <= 0 {
		cfg.MaxOutputTokens = 2048
	}
	if cfg.ContextWindow <= 0 {
		cfg.ContextWindow = 32768
	}
	if cfg.Budget <= 0 {
		cfg.Budget = 3 * time.Minute
	}
	return cfg
}

// runBudget 是可暂停的整轮执行预算。等待审批期间调用 pause 停止扣减。
type runBudget struct {
	mu        sync.Mutex
	remaining time.Duration
	since     time.Time
	running   bool
}

func newRunBudget(total time.Duration) *runBudget {
	return &runBudget{remaining: total, since: time.Now(), running: true}
}

func (b *runBudget) pause() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return
	}
	b.remaining -= time.Since(b.since)
	if b.remaining < 0 {
		b.remaining = 0
	}
	b.running = false
}

func (b *runBudget) resume() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return
	}
	b.since = time.Now()
	b.running = true
}

func (b *runBudget) left() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return b.remaining
	}
	left := b.remaining - time.Since(b.since)
	if left < 0 {
		return 0
	}
	return left
}

func (b *runBudget) expired() bool { return b.left() <= 0 }

func (b *runBudget) context(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, b.left())
}
