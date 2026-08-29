package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/tool/registry"
	"gooncall-agent/internal/tool/runbook"
)

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
		toolName:    "runbook.search",
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
