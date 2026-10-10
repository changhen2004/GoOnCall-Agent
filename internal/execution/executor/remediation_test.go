package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/execution/postmortem"
	"gooncall-agent/internal/execution/verifier"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/tool/deployment"
)

func httpRemediation(t *testing.T, status int, metrics verifier.Metrics) (*Remediation, *incidentservice.Service, *repository.MemoryRun, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		if status >= 200 && status < 300 {
			_, _ = w.Write([]byte(`{"operation_id":"op-recovery"}`))
		}
	}))
	t.Cleanup(server.Close)

	httpExecutor, err := deployment.NewHTTPExecutor(deployment.HTTPExecutorConfig{URL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewHTTPExecutor() error = %v", err)
	}
	restart := deployment.New(nil, nil).WithHTTPExecutor(httpExecutor).WithAllowedTargets([]string{"worker"})
	incidentSvc := incidentservice.New(repository.NewMemory())
	runRepo := repository.NewMemoryRun()
	r := NewRemediation(restart, verifier.New(verifier.DefaultConfig()), &verifier.MockGatherer{Metrics: metrics},
		incidentSvc, runRepo, postmortem.New(), agentruntime.NewStreamBroker())
	return r, incidentSvc, runRepo, &calls
}

func setup(metrics verifier.Metrics) (*Remediation, *incidentservice.Service, *repository.MemoryRun) {
	incidentSvc := incidentservice.New(repository.NewMemory())
	runRepo := repository.NewMemoryRun()

	restart := deployment.New(nil, nil)
	v := verifier.New(verifier.DefaultConfig())
	gatherer := &verifier.MockGatherer{Metrics: metrics}
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
	// 严格状态机：走到 MITIGATING（处置执行前）
	for _, next := range []model.Status{model.StatusWaitingApproval, model.StatusMitigating} {
		if _, err := incidentSvc.MoveTo(context.Background(), inc.ID, next); err != nil {
			t.Fatalf("MoveTo(%s) error = %v", next, err)
		}
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
	if err := json.Unmarshal([]byte(out.Output), &res); err != nil {
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

func TestRemediation_ExplicitActionFailureMarksIncidentFailed(t *testing.T) {
	r, incidentSvc, runRepo, calls := httpRemediation(t, http.StatusInternalServerError, verifier.Metrics{})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	ctx := approval.WithApprovalID(context.Background(), "ap_failed")
	if _, err := r.Execute(ctx, "restart_worker", `{"target":"worker"}`, "run_1"); err == nil {
		t.Fatal("Execute() error = nil, want deployment failure")
	}
	if calls.Load() != 1 {
		t.Fatalf("deployment calls = %d, want 1", calls.Load())
	}
	got, _ := incidentSvc.Get(context.Background(), incidentID)
	if got.Status != model.StatusFailed {
		t.Fatalf("incident status = %s, want FAILED after explicit action failure", got.Status)
	}
	if _, err := r.Execute(ctx, "restart_worker", `{"target":"worker"}`, "run_1"); err == nil {
		t.Fatal("Execute(FAILED action) error = nil, want terminal-state rejection")
	}
	if calls.Load() != 1 {
		t.Fatalf("deployment calls after retry = %d, want 1", calls.Load())
	}
}

func TestRemediation_PersistsPostmortemEvent(t *testing.T) {
	r, incidentSvc, runRepo := setup(verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)
	ctx := approval.WithApprovalID(context.Background(), "ap_postmortem")
	if _, err := r.Execute(ctx, "restart_worker", `{"target":"worker"}`, "run_1"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	events, err := incidentSvc.ListEvents(context.Background(), incidentID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	for _, event := range events {
		if event.EventType == "POSTMORTEM_GENERATED" {
			if event.Payload["approval_id"] != "ap_postmortem" {
				t.Fatalf("postmortem approval_id = %v, want ap_postmortem", event.Payload["approval_id"])
			}
			if postmortem, _ := event.Payload["postmortem"].(string); strings.Contains(postmortem, "## Prevention") {
				return
			}
			t.Fatalf("postmortem event payload = %+v", event.Payload)
		}
	}
	t.Fatalf("events = %+v, want POSTMORTEM_GENERATED", events)
}

func TestRemediation_VerifyFailed(t *testing.T) {
	r, incidentSvc, runRepo := setup(verifier.Metrics{ConsumerCount: 0, QueueDepth: 5000, ErrorRate: 0.05})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	out, err := r.Execute(context.Background(), "restart_worker", `{"target":"worker"}`, "run_1")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var res RemediationResult
	_ = json.Unmarshal([]byte(out.Output), &res)
	if res.Verified || res.Status != "verify_failed" {
		t.Fatalf("result = %+v", res)
	}

	got, _ := incidentSvc.Get(context.Background(), incidentID)
	if got.Status != model.StatusFailed {
		t.Fatalf("incident status = %s, want FAILED when verification fails", got.Status)
	}
	if _, err := r.Execute(context.Background(), "restart_worker", `{"target":"worker"}`, "run_1"); err == nil {
		t.Fatal("Execute(FAILED verification) error = nil, want terminal-state rejection")
	}
}

// scriptedGatherer 按脚本返回指标或错误。
type scriptedGatherer struct {
	metrics []verifier.Metrics
	errs    []error
	calls   int
}

func (g *scriptedGatherer) Gather(_ context.Context, _ string) (verifier.Metrics, error) {
	i := g.calls
	g.calls++
	if i < len(g.errs) && g.errs[i] != nil {
		return verifier.Metrics{}, g.errs[i]
	}
	if i < len(g.metrics) {
		return g.metrics[i], nil
	}
	return verifier.Metrics{}, errors.New("script exhausted")
}

// newScriptedRemediation 用脚本化采集器替换 MockGatherer。
func newScriptedRemediation(t *testing.T, g verifier.MetricGatherer) (*Remediation, *incidentservice.Service, *repository.MemoryRun) {
	t.Helper()
	incidentSvc := incidentservice.New(repository.NewMemory())
	runRepo := repository.NewMemoryRun()
	r := NewRemediation(deployment.New(nil, nil), verifier.New(verifier.DefaultConfig()), g, incidentSvc, runRepo, postmortem.New(), agentruntime.NewStreamBroker())
	return r, incidentSvc, runRepo
}

// TestRemediation_TransitionFailureStops 状态迁移失败必须立即停止：不验证、不关闭 Incident。
func TestRemediation_TransitionFailureStops(t *testing.T) {
	r, incidentSvc, runRepo := setup(verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001})
	ctx := context.Background()

	inc, _, err := incidentSvc.Create(ctx, incidentservice.CreateIncidentInput{Service: "svc", Title: "t"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Incident 停在 OPEN：无法 MITIGATING -> VERIFYING。
	now := time.Now()
	_ = runRepo.CreateRun(ctx, &model.AgentRun{
		ID: "run_1", IncidentID: inc.ID, AgentType: "diagnosis", Status: model.RunRunning, StartedAt: now,
	})

	if _, err := r.Execute(ctx, "restart_worker", `{"target":"worker"}`, "run_1"); err == nil {
		t.Fatal("Execute() should fail when the incident transition is invalid")
	}
	got, _ := incidentSvc.Get(ctx, inc.ID)
	if got.Status != model.StatusOpen {
		t.Fatalf("incident status = %s, want OPEN (unchanged)", got.Status)
	}
}

// TestRemediation_GatherErrorDoesNotResolve 采集错误进 FAILED，绝不能 RESOLVED。
func TestRemediation_GatherErrorDoesNotResolve(t *testing.T) {
	g := &scriptedGatherer{errs: []error{errors.New("prometheus query returned no data")}}
	r, incidentSvc, runRepo := newScriptedRemediation(t, g)
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	out, err := r.Execute(context.Background(), "restart_worker", `{"target":"worker"}`, "run_1")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var res RemediationResult
	if err := json.Unmarshal([]byte(out.Output), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Verified || res.Status != "verify_failed" || res.Reason == "" {
		t.Fatalf("result = %+v, want verify_failed with a reason", res)
	}
	got, _ := incidentSvc.Get(context.Background(), incidentID)
	if got.Status == model.StatusResolved {
		t.Fatal("incident must NOT be RESOLVED when gathering fails")
	}
	if got.Status != model.StatusFailed {
		t.Fatalf("incident status = %s, want FAILED", got.Status)
	}
}

func TestRemediation_VerificationCancelFailsIncident(t *testing.T) {
	ok := verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001}
	r, incidentSvc, runRepo := newScriptedRemediation(t, &scriptedGatherer{metrics: []verifier.Metrics{ok, ok}})
	r.WithVerifyConfig(verifier.RunConfig{Attempts: 3, RequiredSuccesses: 3, Interval: time.Second})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	out, err := r.Execute(ctx, "restart_worker", `{"target":"worker"}`, "run_1")
	if err != nil {
		t.Fatalf("Execute() error = %v, want a persisted verification failure", err)
	}
	var res RemediationResult
	if err := json.Unmarshal([]byte(out.Output), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Status != "verify_failed" || res.Reason == "" {
		t.Fatalf("result = %+v, want verify_failed with a reason", res)
	}
	got, _ := incidentSvc.Get(context.Background(), incidentID)
	if got.Status != model.StatusFailed {
		t.Fatalf("incident status = %s, want FAILED", got.Status)
	}
}

// TestRemediation_RequiresConsecutiveSuccesses 只有连续 N 次通过才 RESOLVED；中途失败重置计数。
func TestRemediation_RequiresConsecutiveSuccesses(t *testing.T) {
	ok := verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001}
	bad := verifier.Metrics{ConsumerCount: 0, QueueDepth: 5000, ErrorRate: 0.05}
	g := &scriptedGatherer{metrics: []verifier.Metrics{ok, bad, ok}}
	r, incidentSvc, runRepo := newScriptedRemediation(t, g)
	r.WithVerifyConfig(verifier.RunConfig{Attempts: 3, RequiredSuccesses: 2})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	out, err := r.Execute(context.Background(), "restart_worker", `{"target":"worker"}`, "run_1")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var res RemediationResult
	_ = json.Unmarshal([]byte(out.Output), &res)
	if res.Verified || res.Status != "verify_failed" {
		t.Fatalf("result = %+v, want verify_failed (streak reset by first attempt)", res)
	}
	if got, _ := incidentSvc.Get(context.Background(), incidentID); got.Status == model.StatusResolved {
		t.Fatal("incident must NOT be RESOLVED with a broken success streak")
	}
}

// TestRemediation_ConsecutiveSuccessesResolves 连续通过达到配置值才关闭 Incident，复盘用真实数据。
func TestRemediation_ConsecutiveSuccessesResolves(t *testing.T) {
	ok := verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001}
	g := &scriptedGatherer{metrics: []verifier.Metrics{ok, ok}}
	r, incidentSvc, runRepo := newScriptedRemediation(t, g)
	r.WithVerifyConfig(verifier.RunConfig{Attempts: 3, RequiredSuccesses: 2})
	incidentID := createIncidentAndRun(t, incidentSvc, runRepo)

	out, err := r.Execute(context.Background(), "restart_worker", `{"target":"worker"}`, "run_1")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var res RemediationResult
	_ = json.Unmarshal([]byte(out.Output), &res)
	if !res.Verified || res.Status != "completed" {
		t.Fatalf("result = %+v, want completed", res)
	}
	if got, _ := incidentSvc.Get(context.Background(), incidentID); got.Status != model.StatusResolved {
		t.Fatalf("incident status = %s, want RESOLVED", got.Status)
	}
	if strings.Contains(res.Postmortem, "worker restart 后指标恢复") {
		t.Fatalf("postmortem still contains the hard-coded claim: %s", res.Postmortem)
	}
	// 复盘证据必须来自真实验证明细。
	if !strings.Contains(res.Postmortem, "consumers=5") {
		t.Fatalf("postmortem missing real check detail: %s", res.Postmortem)
	}
}
