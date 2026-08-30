package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/tool/registry"
	"gooncall-agent/internal/tool/runbook"
)

func TestRuntime_DiagnoseRecordsRunAndEvents(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rabbitmq.md"), []byte(`# 消费者异常

检查 consumer count。
`), 0o600)
	runbookTool := runbook.New(dir)
	einoRunbook, err := runbookTool.EinoTool()
	if err != nil {
		t.Fatalf("EinoTool: %v", err)
	}
	reg := registry.New()
	_ = reg.Register(einoRunbook, registry.RiskLow)

	fake := &fakeModel{
		toolName:    "runbook_search",
		toolArgs:    `{"query":"rabbitmq"}`,
		finalAnswer: "根因：消费者异常",
	}

	runRepo := repository.NewMemoryRun()
	broker := NewStreamBroker()
	rt := New(fake, reg, 15).WithRunRecording(runRepo, broker)

	inc := &incidentmodel.Incident{
		ID: "inc_1", Service: "svc", Title: "rabbitmq backlog", Status: incidentmodel.StatusInvestigating,
	}

	result, err := rt.Diagnose(context.Background(), inc, "你是诊断 agent")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if result.RunID == "" {
		t.Fatal("run id should not be empty")
	}

	// Run 状态
	run, err := runRepo.GetRun(context.Background(), result.RunID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if run.Status != incidentmodel.RunCompleted {
		t.Fatalf("run status = %s, want COMPLETED", run.Status)
	}
	if run.IncidentID != "inc_1" {
		t.Fatalf("run incident = %s, want inc_1", run.IncidentID)
	}

	// ToolCall 记录
	calls, _ := runRepo.ListToolCalls(context.Background(), result.RunID)
	if len(calls) != 1 || calls[0].ToolName != "runbook_search" {
		t.Fatalf("tool calls = %+v", calls)
	}
	if calls[0].Status != "COMPLETED" {
		t.Fatalf("tool call status = %s", calls[0].Status)
	}

	// Step 记录
	steps, _ := runRepo.ListSteps(context.Background(), result.RunID)
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}

	// SSE 事件（历史回放）
	ch, cancel := broker.Subscribe(result.RunID)
	defer cancel()

	seen := map[string]bool{}
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-ch:
			seen[ev.Type] = true
			if ev.Type == "done" {
				goto verify
			}
		case <-deadline:
			t.Fatalf("timed out waiting for done, seen: %v", seen)
		}
	}
verify:
	for _, want := range []string{"run.started", "tool.started", "tool.completed", "done"} {
		if !seen[want] {
			t.Fatalf("missing event %s, seen: %v", want, seen)
		}
	}
}
