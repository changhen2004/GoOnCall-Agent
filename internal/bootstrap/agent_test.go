package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	agentruntime "gooncall-agent/internal/agent/runtime"
	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/tool/registry"
)

type finalDiagnosisModel struct{}

func (finalDiagnosisModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
}

func (finalDiagnosisModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := (finalDiagnosisModel{}).Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func (finalDiagnosisModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return finalDiagnosisModel{}, nil
}

func TestRunDiagnosis_DoesNotReplaceOldRunningRun(t *testing.T) {
	ctx := context.Background()
	incidents := incidentservice.New(repository.NewMemory())
	runs := repository.NewMemoryRun()
	inc, _, err := incidents.Create(ctx, incidentservice.CreateIncidentInput{Service: "svc", Title: "stale run"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := incidents.Analyze(ctx, inc.ID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if err := runs.CreateRun(ctx, &incidentmodel.AgentRun{
		ID: "run_stale", IncidentID: inc.ID, AgentType: "diagnosis", Status: incidentmodel.RunRunning,
		StartedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("CreateRun(stale) error = %v", err)
	}

	rt := agentruntime.New(finalDiagnosisModel{}, registry.New(), 5).
		WithRunRecording(runs, agentruntime.NewStreamBroker()).
		WithRunTimeout(time.Minute)
	if err := RunDiagnosis(ctx, incidents, runs, rt, "prompt", inc.ID); err != nil {
		t.Fatalf("RunDiagnosis() error = %v", err)
	}

	got, err := runs.ListRunsByIncident(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListRunsByIncident() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("runs = %+v, want the existing run only", got)
	}
	if got[0].ID != "run_stale" || got[0].Status != incidentmodel.RunRunning {
		t.Fatalf("run = %+v, want the original RUNNING run unchanged", got[0])
	}
}
