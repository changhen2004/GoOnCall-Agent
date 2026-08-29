package executor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/execution/postmortem"
	"gooncall-agent/internal/execution/verifier"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/tool/deployment"
)

func setup(metrics verifier.Metrics) (*Remediation, *incidentservice.Service, *repository.MemoryRun) {
	incidentSvc := incidentservice.New(repository.NewMemory())
	runRepo := repository.NewMemoryRun()

	restart := deployment.New(nil, nil)
	v := verifier.New(verifier.DefaultConfig())
	gatherer := &SimulatedGatherer{Metrics: metrics}
	pm := postmortem.New()
	broker := agentruntime.NewStreamBroker()

	return NewRemediation(restart, v, gatherer, incidentSvc, runRepo, pm, broker), incidentSvc, runRepo
}

func createIncidentAndRun(t *testing.T, incidentSvc *incidentservice.Service, runRepo *repository.MemoryRun) string {
	t.Helper()
	inc, _, err := incidentSvc.Create(context.Background(), incidentservice.CreateIncidentInput{Service: "svc", Title: "rabbitmq backlog"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := incidentSvc.Analyze(context.Background(), inc.ID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	now := time.Now()
	_ = runRepo.CreateRun(context.Background(), &model.AgentRun{
		ID: "run_1", IncidentID: inc.ID, AgentType: "diagnosis", Status: model.RunRunning, StartedAt: now,
	})
	return inc.ID
}

func TestRemediation_ExecuteVerifyResolve(t *testing.T) {
	r, incidentSvc, runRepo := setup(verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	out, err := r.Execute(context.Background(), "restart_worker", `{"target":"worker"}`, "run_1")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var res RemediationResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !res.Verified || res.Status != "completed" {
		t.Fatalf("result = %+v", res)
	}
	if res.IncidentID != incidentID {
		t.Fatalf("incident id = %s, want %s", res.IncidentID, incidentID)
	}
	if !strings.Contains(res.Postmortem, incidentID) {
		t.Fatalf("postmortem missing incident id: %s", res.Postmortem)
	}

	got, _ := incidentSvc.Get(context.Background(), incidentID)
	if got.Status != model.StatusResolved {
		t.Fatalf("incident status = %s, want RESOLVED", got.Status)
	}
}

func TestRemediation_VerifyFailed(t *testing.T) {
	r, incidentSvc, runRepo := setup(verifier.Metrics{ConsumerCount: 0, QueueDepth: 5000, ErrorRate: 0.05})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	out, err := r.Execute(context.Background(), "restart_worker", `{"target":"worker"}`, "run_1")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var res RemediationResult
	_ = json.Unmarshal([]byte(out), &res)
	if res.Verified || res.Status != "verify_failed" {
		t.Fatalf("result = %+v", res)
	}

	got, _ := incidentSvc.Get(context.Background(), incidentID)
	if got.Status == model.StatusResolved {
		t.Fatal("incident should NOT be resolved when verification fails")
	}
}
