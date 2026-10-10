package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/tool/registry"
	"gooncall-agent/internal/tool/runbook"
)

type finalModel struct{}

func (finalModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: "diagnosis complete"}, nil
}

func (finalModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := (finalModel{}).Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (finalModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return finalModel{}, nil
}

type terminalFailingRunRepo struct {
	*repository.MemoryRun
	failStep     bool
	failComplete bool
	failFailed   bool
}

func (r *terminalFailingRunRepo) AddStep(ctx context.Context, step *incidentmodel.AgentStep) error {
	if r.failStep {
		return errors.New("step store unavailable")
	}
	return r.MemoryRun.AddStep(ctx, step)
}

func (r *terminalFailingRunRepo) UpdateRun(ctx context.Context, run *incidentmodel.AgentRun) error {
	if run.Status == incidentmodel.RunCompleted && r.failComplete {
		return errors.New("completion store unavailable")
	}
	if run.Status == incidentmodel.RunFailed && r.failFailed {
		return errors.New("failure store unavailable")
	}
	return r.MemoryRun.UpdateRun(ctx, run)
}

// fakeModel 是脚本化的 ToolCallingChatModel：首次返回工具调用，见工具结果后返回最终结论。
type fakeModel struct {
	toolName    string
	toolArgs    string
	finalAnswer string
	calls       int
}

func (f *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.calls++
	hasToolResult := false
	for _, m := range input {
		if m.Role == schema.Tool {
			hasToolResult = true
		}
	}
	if hasToolResult {
		return &schema.Message{Role: schema.Assistant, Content: f.finalAnswer}, nil
	}
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: f.toolName, Arguments: f.toolArgs}},
		},
	}, nil
}

func (f *fakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := f.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (f *fakeModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

func TestRuntime_DiagnoseRunsReActLoop(t *testing.T) {
	// 准备一个真实 runbook 工具
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rabbitmq.md"), []byte("# 消费者异常\n\n检查 consumer count。\n"), 0o600)
	runbookTool := runbook.New(dir)
	einoRunbook, err := runbookTool.EinoTool()
	if err != nil {
		t.Fatalf("runbook EinoTool: %v", err)
	}
	reg := registry.New()
	if err := reg.Register(einoRunbook, registry.RiskLow); err != nil {
		t.Fatalf("register runbook: %v", err)
	}

	// 脚本化模型：先调 runbook.search，再给结论
	fake := &fakeModel{
		toolName:    "runbook_search",
		toolArgs:    `{"query":"rabbitmq consumer"}`,
		finalAnswer: "根因：RabbitMQ 消费者连接异常（置信度 0.9）",
	}

	rt := New(fake, reg, 15)
	inc := &incidentmodel.Incident{
		Service:   "gocommunity",
		Severity:  "HIGH",
		Title:     "rabbitmq backlog",
		AlertName: "HighQueueDepth",
		Status:    incidentmodel.StatusInvestigating,
	}

	result, err := rt.Diagnose(context.Background(), inc, "你是诊断 agent")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}

	if result.Conclusion != fake.finalAnswer {
		t.Fatalf("conclusion = %q, want %q", result.Conclusion, fake.finalAnswer)
	}
	if fake.calls != 2 {
		t.Fatalf("model calls = %d, want 2 (tool call + final)", fake.calls)
	}
}

func TestRuntime_NewDefaultsMaxSteps(t *testing.T) {
	fake := &fakeModel{finalAnswer: "ok"}
	rt := New(fake, registry.New(), 0)
	if rt.maxSteps != 15 {
		t.Fatalf("maxSteps = %d, want 15", rt.maxSteps)
	}
}

// blockingModel 阻塞到 ctx 超时，用于验证整轮诊断超时。
type blockingModel struct{}

func (blockingModel) Generate(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (blockingModel) Stream(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, context.DeadlineExceeded
}

func (blockingModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return blockingModel{}, nil
}

func TestRuntime_DiagnoseRunTimeout(t *testing.T) {
	rt := New(blockingModel{}, registry.New(), 15).WithRunTimeout(30 * time.Millisecond)
	inc := &incidentmodel.Incident{
		Service:   "gocommunity",
		Severity:  "HIGH",
		Title:     "rabbitmq backlog",
		AlertName: "HighQueueDepth",
		Status:    incidentmodel.StatusInvestigating,
	}

	_, err := rt.Diagnose(context.Background(), inc, "你是诊断 agent")
	if err == nil {
		t.Fatal("Diagnose() should fail when run timeout expires")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
}

func TestRuntime_DiagnoseReturnsStepPersistenceError(t *testing.T) {
	repo := &terminalFailingRunRepo{MemoryRun: repository.NewMemoryRun(), failStep: true}
	rt := New(finalModel{}, registry.New(), 15).WithRunRecording(repo, NewStreamBroker())
	inc := &incidentmodel.Incident{ID: "inc_step_error", Title: "t", Status: incidentmodel.StatusInvestigating}

	_, err := rt.Diagnose(context.Background(), inc, "prompt")
	if err == nil || !strings.Contains(err.Error(), "step store unavailable") {
		t.Fatalf("Diagnose() error = %v, want step persistence error", err)
	}
}

func TestRuntime_DiagnoseReturnsCompletionPersistenceError(t *testing.T) {
	repo := &terminalFailingRunRepo{MemoryRun: repository.NewMemoryRun(), failComplete: true}
	rt := New(finalModel{}, registry.New(), 15).WithRunRecording(repo, NewStreamBroker())
	inc := &incidentmodel.Incident{ID: "inc_complete_error", Title: "t", Status: incidentmodel.StatusInvestigating}

	_, err := rt.Diagnose(context.Background(), inc, "prompt")
	if err == nil || !strings.Contains(err.Error(), "completion store unavailable") {
		t.Fatalf("Diagnose() error = %v, want completion persistence error", err)
	}
}

func TestRuntime_DiagnoseReportsFailurePersistenceError(t *testing.T) {
	repo := &terminalFailingRunRepo{MemoryRun: repository.NewMemoryRun(), failFailed: true}
	rt := New(blockingModel{}, registry.New(), 15).
		WithRunRecording(repo, NewStreamBroker()).
		WithRunTimeout(10 * time.Millisecond)
	inc := &incidentmodel.Incident{ID: "inc_fail_error", Title: "t", Status: incidentmodel.StatusInvestigating}

	_, err := rt.Diagnose(context.Background(), inc, "prompt")
	if err == nil || !strings.Contains(err.Error(), "failure store unavailable") {
		t.Fatalf("Diagnose() error = %v, want failure persistence error", err)
	}
}
