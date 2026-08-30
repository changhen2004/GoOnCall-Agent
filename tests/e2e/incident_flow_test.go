// Package e2e 提供端到端流程测试（模拟 Create -> Agent -> Approval -> Execute -> Verify -> Resolve）。
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/execution/executor"
	"gooncall-agent/internal/execution/policy"
	"gooncall-agent/internal/execution/postmortem"
	"gooncall-agent/internal/execution/verifier"
	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/tool/deployment"
	"gooncall-agent/internal/tool/registry"
	"gooncall-agent/internal/tool/runbook"
)

// fakeModel 脚本化 LLM：先调 runbook.search，再给结论。
type fakeModel struct {
	calls int
}

func (f *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	f.calls++
	for _, m := range input {
		if m.Role == schema.Tool {
			return &schema.Message{Role: schema.Assistant, Content: "根因：消费者连接异常，建议重启 worker"}, nil
		}
	}
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "call_1", Type: "function", Function: schema.FunctionCall{Name: "runbook_search", Arguments: `{"query":"rabbitmq consumer"}`}},
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

func (f *fakeModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

type testApp struct {
	incidentSvc *incidentservice.Service
	runRepo     *repository.MemoryRun
	broker      *agentruntime.StreamBroker
	approvalSvc *approval.Service
	runtime     *agentruntime.Runtime
}

func setup(t *testing.T) *testApp {
	t.Helper()
	ctx := context.Background()

	incidentRepo := repository.NewMemory()
	runRepo := repository.NewMemoryRun()
	approvalRepo := repository.NewMemoryApproval()
	broker := agentruntime.NewStreamBroker()
	incidentSvc := incidentservice.New(incidentRepo)

	// 工具注册表：runbook.search + worker.restart
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rabbitmq.md"), []byte(`# 消费者异常

检查 consumer count。
`), 0o600)
	reg := registry.New()
	if rb, err := runbook.New(dir).EinoTool(); err == nil {
		_ = reg.Register(rb, registry.RiskLow)
	}

	policyEngine := policy.New(true)
	restartTool := deployment.New(nil, policyEngine)
	gatherer := &verifier.MockGatherer{Metrics: verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001}}
	remediation := executor.NewRemediation(restartTool, verifier.New(verifier.DefaultConfig()), gatherer, incidentSvc, runRepo, postmortem.New(), broker)
	approvalSvc := approval.New(approvalRepo, broker, remediation).WithIncidentState(incidentSvc, runRepo)
	restartTool.SetApproval(approvalSvc)

	if rt, err := restartTool.EinoTool(); err == nil {
		_ = reg.Register(rt, registry.RiskMedium)
	}

	rt := agentruntime.New(&fakeModel{}, reg, 15).WithRunRecording(runRepo, broker)

	_ = ctx
	return &testApp{incidentSvc: incidentSvc, runRepo: runRepo, broker: broker, approvalSvc: approvalSvc, runtime: rt}
}

func TestIncidentFlow(t *testing.T) {
	ctx := context.Background()
	app := setup(t)

	// 1. 创建 Incident
	inc, _, err := app.incidentSvc.Create(ctx, incidentservice.CreateIncidentInput{Service: "gocommunity", Title: "rabbitmq backlog", AlertName: "HighQueueDepth"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// 2. 分析（OPEN -> INVESTIGATING）
	inc, err = app.incidentSvc.Analyze(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// 3. Agent Run（诊断）
	result, err := app.runtime.Diagnose(ctx, inc, "你是诊断 agent")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if result.RunID == "" {
		t.Fatal("run id should not be empty")
	}

	// 4. 审批请求（Agent 建议重启 worker）-> WAITING_APPROVAL
	ap, err := app.approvalSvc.Request(ctx, result.RunID, "", "restart_worker", `{"target":"worker"}`, "consumer count dropped")
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	got, _ := app.incidentSvc.Get(ctx, inc.ID)
	if got.Status != incidentmodel.StatusWaitingApproval {
		t.Fatalf("status after approval request = %s, want WAITING_APPROVAL", got.Status)
	}

	// 5. 批准 -> 执行 -> 验证 -> 关闭
	if _, err := app.approvalSvc.Approve(ctx, ap.ID, "admin"); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	// 6. 断言最终状态 RESOLVED
	got, _ = app.incidentSvc.Get(ctx, inc.ID)
	if got.Status != incidentmodel.StatusResolved {
		t.Fatalf("final status = %s, want RESOLVED", got.Status)
	}
	if got.ResolvedAt == nil {
		t.Fatal("resolved_at should be set")
	}

	// 7. 断言 Agent Run 与 ToolCall 已记录
	run, _ := app.runRepo.GetRun(ctx, result.RunID)
	if run.Status != incidentmodel.RunCompleted {
		t.Fatalf("run status = %s, want COMPLETED", run.Status)
	}
	calls, _ := app.runRepo.ListToolCalls(ctx, result.RunID)
	if len(calls) == 0 {
		t.Fatal("expected at least one tool call recorded")
	}

	_ = time.Now
}
