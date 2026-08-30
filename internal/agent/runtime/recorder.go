package runtime

import (
	"context"
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
func (r *Recorder) Step(ctx context.Context, agent, action, input, output string, durationMs int64) {
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
	_ = r.runRepo.AddStep(ctx, step)
	r.publish("agent.thinking", map[string]any{"agent": agent, "action": action, "step_index": r.stepIdx})
}

// ToolStarted 发布 tool.started 事件。
func (r *Recorder) ToolStarted(name string) {
	r.publish("tool.started", map[string]any{"tool": name})
}

// ToolCompleted 记录 ToolCall 并发布 tool.completed 事件。
func (r *Recorder) ToolCompleted(ctx context.Context, name, risk, args, result string, callErr error, durationMs int64) {
	status := "COMPLETED"
	if callErr != nil {
		status = "FAILED"
	}
	call := &incidentmodel.ToolCall{
		ID:         "call_" + uuid.NewString(),
		RunID:      r.run.ID,
		ToolName:   name,
		RiskLevel:  risk,
		Arguments:  args,
		Result:     result,
		Status:     status,
		DurationMs: durationMs,
		CreatedAt:  time.Now(),
	}
	_ = r.runRepo.AddToolCall(ctx, call)
	metrics.ToolCallsTotal.WithLabelValues(name, status).Inc()
	r.publish("tool.completed", map[string]any{"tool": name, "status": status, "duration_ms": durationMs})
}

// Complete 将 Run 置为 COMPLETED 并发布 done 事件。
func (r *Recorder) Complete(ctx context.Context) {
	now := time.Now()
	r.run.Status = incidentmodel.RunCompleted
	r.run.FinishedAt = &now
	_ = r.runRepo.UpdateRun(ctx, r.run)
	metrics.AgentRunsTotal.WithLabelValues(r.run.AgentType, string(incidentmodel.RunCompleted)).Inc()
	r.publish("done", map[string]any{"run_id": r.run.ID})
}

// Fail 将 Run 置为 FAILED 并发布 done 事件。
func (r *Recorder) Fail(ctx context.Context, err error) {
	now := time.Now()
	r.run.Status = incidentmodel.RunFailed
	r.run.FinishedAt = &now
	r.run.Error = err.Error()
	_ = r.runRepo.UpdateRun(ctx, r.run)
	metrics.AgentRunsTotal.WithLabelValues(r.run.AgentType, string(incidentmodel.RunFailed)).Inc()
	r.publish("done", map[string]any{"run_id": r.run.ID, "error": err.Error()})
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
	t.recorder.ToolStarted(t.name)
	start := time.Now()

	timeout := t.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := t.inner.InvokableRun(callCtx, args, opts...)
	t.recorder.ToolCompleted(ctx, t.name, t.risk, args, result, err, time.Since(start).Milliseconds())
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
