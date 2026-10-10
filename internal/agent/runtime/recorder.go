package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/observability/metrics"
	"gooncall-agent/internal/tool/registry"
)

// ErrMaxToolCalls 表示工具调用次数超过上限。
var ErrMaxToolCalls = fmt.Errorf("tool call count exceeds max")

// Recorder 记录 Agent Run 生命周期（Run/Step/ToolCall），并发布 SSE 事件。
type Recorder struct {
	runRepo      repository.RunRepository
	broker       *StreamBroker
	run          *incidentmodel.AgentRun
	stepIdx      int
	maxToolCalls int
	toolCalls    int
}

// NewRecorder 创建 Recorder 并初始化一个 RUNNING 状态的 AgentRun。
func NewRecorder(runRepo repository.RunRepository, broker *StreamBroker, inc *incidentmodel.Incident, agentType string, maxToolCalls int) *Recorder {
	run := &incidentmodel.AgentRun{
		ID:         "run_" + uuid.NewString(),
		IncidentID: inc.ID,
		AgentType:  agentType,
		Status:     incidentmodel.RunRunning,
		Goal:       inc.Title,
		StartedAt:  time.Now(),
	}
	return &Recorder{runRepo: runRepo, broker: broker, run: run, maxToolCalls: maxToolCalls}
}

// checkToolCall 检查并登记一次工具调用，超过上限返回 ErrMaxToolCalls。
func (r *Recorder) checkToolCall() error {
	r.toolCalls++
	if r.maxToolCalls > 0 && r.toolCalls > r.maxToolCalls {
		return ErrMaxToolCalls
	}
	return nil
}

// RunID 返回当前 Run 的 ID。
func (r *Recorder) RunID() string { return r.run.ID }

// Start 持久化 Run 并发布 run.started 事件。
func (r *Recorder) Start(ctx context.Context) error {
	if err := r.runRepo.CreateRun(ctx, r.run); err != nil {
		return err
	}
	metrics.AgentRunsTotal.WithLabelValues(r.run.AgentType, string(incidentmodel.RunRunning)).Inc()
	r.publish("run.started", map[string]any{"run_id": r.run.ID, "incident_id": r.run.IncidentID})
	return nil
}

// Step 记录一个 Agent 步骤。
func (r *Recorder) Step(ctx context.Context, agent, action, input, output string, durationMs int64) error {
	r.stepIdx++
	step := &incidentmodel.AgentStep{
		ID:        "step_" + uuid.NewString(),
		RunID:     r.run.ID,
		StepIndex: r.stepIdx,
		Agent:     agent,
		Action:    action,
		Status:    "COMPLETED",
		Input:     input,
		Output:    output,
		Duration:  durationMs,
	}
	if err := r.runRepo.AddStep(ctx, step); err != nil {
		return fmt.Errorf("persist run step: %w", err)
	}
	r.publish("agent.thinking", map[string]any{"agent": agent, "action": action, "step_index": r.stepIdx})
	return nil
}

// ToolStarted 发布 tool.started 事件。
func (r *Recorder) ToolStarted(name string) {
	r.publish("tool.started", map[string]any{"tool": name})
}

// StartToolCall 在调用工具**之前**持久化一条 RUNNING ToolCall。
//
// 顺序很重要：工具内部可能创建引用 callID 的 Approval（Approval.ToolCallID），
// 记录必须先存在，否则数据库故障会留下指向不存在 ToolCall 的孤立审批。
// 写入失败返回错误，调用方不得继续调用工具。
func (r *Recorder) StartToolCall(ctx context.Context, callID, name, risk, args string) error {
	call := &incidentmodel.ToolCall{
		ID:        callID,
		RunID:     r.run.ID,
		ToolName:  name,
		RiskLevel: risk,
		Arguments: args,
		Status:    "RUNNING",
		CreatedAt: time.Now(),
	}
	if err := r.runRepo.AddToolCall(ctx, call); err != nil {
		return fmt.Errorf("persist tool call %s: %w", callID, err)
	}
	return nil
}

// CompleteToolCall 在工具返回后落结果并发布事件。
//
// 持久化失败必须向上抛：审计链断裂不能被静默吞掉。
func (r *Recorder) CompleteToolCall(ctx context.Context, callID, name, result string, callErr error, durationMs int64) error {
	status := "COMPLETED"
	if callErr != nil {
		status = "FAILED"
	}
	persistCtx, cancel := recorderPersistenceContext(ctx)
	defer cancel()
	if err := r.runRepo.UpdateToolCall(persistCtx, &incidentmodel.ToolCall{
		ID:         callID,
		RunID:      r.run.ID,
		Result:     result,
		Status:     status,
		DurationMs: durationMs,
	}); err != nil {
		return fmt.Errorf("persist tool call %s result: %w", callID, err)
	}
	metrics.ToolCallsTotal.WithLabelValues(name, status).Inc()
	r.publish("tool.completed", map[string]any{"tool": name, "status": status, "duration_ms": durationMs})
	return nil
}

// Complete 将 Run 置为 COMPLETED 并发布 done 事件。
func (r *Recorder) Complete(ctx context.Context) error {
	now := time.Now()
	r.run.Status = incidentmodel.RunCompleted
	r.run.FinishedAt = &now
	persistCtx, cancel := recorderPersistenceContext(ctx)
	defer cancel()
	if err := r.runRepo.UpdateRun(persistCtx, r.run); err != nil {
		return fmt.Errorf("persist completed run: %w", err)
	}
	metrics.AgentRunsTotal.WithLabelValues(r.run.AgentType, string(incidentmodel.RunCompleted)).Inc()
	r.publish("done", map[string]any{"run_id": r.run.ID})
	return nil
}

// Fail 将 Run 置为 FAILED 并发布 done 事件。
func (r *Recorder) Fail(ctx context.Context, cause error) error {
	now := time.Now()
	r.run.Status = incidentmodel.RunFailed
	r.run.FinishedAt = &now
	r.run.Error = cause.Error()
	persistCtx, cancel := recorderPersistenceContext(ctx)
	defer cancel()
	if err := r.runRepo.UpdateRun(persistCtx, r.run); err != nil {
		return fmt.Errorf("persist failed run: %w", err)
	}
	metrics.AgentRunsTotal.WithLabelValues(r.run.AgentType, string(incidentmodel.RunFailed)).Inc()
	r.publish("done", map[string]any{"run_id": r.run.ID, "error": cause.Error()})
	return nil
}

func recorderPersistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (r *Recorder) publish(typ string, data map[string]any) {
	if r.broker != nil {
		r.broker.Publish(r.run.ID, StreamEvent{Type: typ, Data: data})
	}
}

// recordingTool 包装 Eino 工具，在调用前后记录 ToolCall 并发布 SSE 事件。
type recordingTool struct {
	inner    tool.InvokableTool
	name     string
	risk     string
	recorder *Recorder
	timeout  time.Duration
}

func (t *recordingTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t *recordingTool) InvokableRun(ctx context.Context, args string, opts ...tool.Option) (string, error) {
	if err := t.recorder.checkToolCall(); err != nil {
		return "", err
	}
	// ToolCall ID 必须在调用工具前生成：工具可能用它创建关联记录（如 Approval.ToolCallID）。
	callID := "call_" + uuid.NewString()
	// 先落 RUNNING 记录：工具执行期间创建的 Approval 必须能引用到已存在的 ToolCall。
	if err := t.recorder.StartToolCall(ctx, callID, t.name, t.risk, args); err != nil {
		return "", err
	}
	t.recorder.ToolStarted(t.name)
	start := time.Now()

	timeout := t.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(WithToolCallID(ctx, callID), timeout)
	defer cancel()

	result, err := t.inner.InvokableRun(callCtx, args, opts...)
	if perr := t.recorder.CompleteToolCall(ctx, callID, t.name, result, err, time.Since(start).Milliseconds()); perr != nil {
		// 审计写入失败：不能假装这次调用正常完成。
		return result, errors.Join(err, perr)
	}
	return result, err
}

// wrapTools 将注册表内所有工具包装为带记录能力的工具。
func wrapTools(reg *registry.Registry, rec *Recorder, timeout time.Duration) []tool.BaseTool {
	all := reg.All()
	out := make([]tool.BaseTool, 0, len(all))
	for _, rt := range all {
		out = append(out, &recordingTool{
			inner:    rt.Tool,
			name:     rt.Name,
			risk:     string(rt.RiskLevel),
			recorder: rec,
			timeout:  timeout,
		})
	}
	return out
}
