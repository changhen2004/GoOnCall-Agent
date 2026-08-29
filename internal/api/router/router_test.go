package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
)

type fakeDiagnoser struct {
	conclusion string
}

func (f *fakeDiagnoser) Diagnose(_ context.Context, _ *model.Incident, _ string) (*agentruntime.DiagnosisResult, error) {
	return &agentruntime.DiagnosisResult{Conclusion: f.conclusion}, nil
}

func newRunHandler() *handler.RunHandler {
	return handler.NewRunHandler(repository.NewMemoryRun(), agentruntime.NewStreamBroker())
}

func newApprovalHandler() *handler.ApprovalHandler {
	approvalSvc := approval.New(repository.NewMemoryApproval(), agentruntime.NewStreamBroker(), nil)
	return handler.NewApprovalHandler(approvalSvc)
}

func newAlertHandler() *handler.AlertHandler {
	return handler.NewAlertHandler(incidentservice.New(repository.NewMemory()))
}

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := incidentservice.New(repository.NewMemory())
	return New(handler.NewIncidentHandler(svc), handler.NewHealthHandler(), newRunHandler(), newApprovalHandler(), newAlertHandler())
}

func newTestRouterWithDiagnoser(d handler.Diagnoser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := incidentservice.New(repository.NewMemory())
	incidentHandler := handler.NewIncidentHandler(svc).WithDiagnoser(d, "你是诊断 agent")
	return New(incidentHandler, handler.NewHealthHandler(), newRunHandler(), newApprovalHandler(), newAlertHandler())
}

func doRequest(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHealthz(t *testing.T) {
	r := newTestRouter()
	w := doRequest(t, r, http.MethodGet, "/healthz", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", w.Code)
	}
}

func TestCreateIncident(t *testing.T) {
	r := newTestRouter()
	w := doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{
		Service: "gocommunity", Title: "rabbitmq backlog", AlertName: "HighQueueDepth",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201, body=%s", w.Code, w.Body.String())
	}

	var resp dto.IncidentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("response id should not be empty")
	}
	if resp.Status != "OPEN" {
		t.Fatalf("status = %q, want OPEN", resp.Status)
	}
	if resp.Fingerprint == "" {
		t.Fatal("fingerprint should not be empty")
	}
}

func TestCreateIncidentDedup(t *testing.T) {
	r := newTestRouter()
	body := dto.CreateIncidentRequest{
		Service: "gocommunity", Title: "first", AlertName: "HighErrorRate",
	}
	first := doRequest(t, r, http.MethodPost, "/api/v1/incidents", body)
	if first.Code != http.StatusCreated {
		t.Fatalf("first create status = %d", first.Code)
	}

	second := doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{
		Service: "gocommunity", Title: "second", AlertName: "HighErrorRate",
	})
	if second.Code != http.StatusOK {
		t.Fatalf("dedup create status = %d, want 200", second.Code)
	}

	var a, b dto.IncidentResponse
	_ = json.Unmarshal(first.Body.Bytes(), &a)
	_ = json.Unmarshal(second.Body.Bytes(), &b)
	if a.ID != b.ID {
		t.Fatalf("dedup returned different ids: %s != %s", a.ID, b.ID)
	}
}

func TestCreateIncidentValidation(t *testing.T) {
	r := newTestRouter()
	w := doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{Service: "svc"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid create status = %d, want 400", w.Code)
	}
}

func TestGetIncident(t *testing.T) {
	r := newTestRouter()
	created := doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{Service: "svc", Title: "t"})
	var inc dto.IncidentResponse
	_ = json.Unmarshal(created.Body.Bytes(), &inc)

	w := doRequest(t, r, http.MethodGet, "/api/v1/incidents/"+inc.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", w.Code)
	}
}

func TestGetIncidentNotFound(t *testing.T) {
	r := newTestRouter()
	w := doRequest(t, r, http.MethodGet, "/api/v1/incidents/missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get missing status = %d, want 404", w.Code)
	}
}

func TestListIncidents(t *testing.T) {
	r := newTestRouter()
	doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{Service: "svc-a", Title: "a"})
	doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{Service: "svc-b", Title: "b"})

	w := doRequest(t, r, http.MethodGet, "/api/v1/incidents", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var resp dto.ListIncidentsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("list total = %d items=%d, want 2/2", resp.Total, len(resp.Items))
	}
}

func TestAnalyzeAndResolveFlow(t *testing.T) {
	r := newTestRouter()
	created := doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{Service: "svc", Title: "t"})
	var inc dto.IncidentResponse
	_ = json.Unmarshal(created.Body.Bytes(), &inc)

	analyzed := doRequest(t, r, http.MethodPost, "/api/v1/incidents/"+inc.ID+"/analyze", nil)
	if analyzed.Code != http.StatusOK {
		t.Fatalf("analyze status = %d", analyzed.Code)
	}
	var a dto.AnalyzeResponse
	_ = json.Unmarshal(analyzed.Body.Bytes(), &a)
	if a.Incident.Status != "INVESTIGATING" {
		t.Fatalf("analyze status = %q, want INVESTIGATING", a.Incident.Status)
	}
	if a.Diagnosis != nil {
		t.Fatalf("unexpected diagnosis without agent: %+v", a.Diagnosis)
	}

	resolved := doRequest(t, r, http.MethodPost, "/api/v1/incidents/"+inc.ID+"/resolve", nil)
	if resolved.Code != http.StatusOK {
		t.Fatalf("resolve status = %d", resolved.Code)
	}
	var res dto.IncidentResponse
	_ = json.Unmarshal(resolved.Body.Bytes(), &res)
	if res.Status != "RESOLVED" || res.ResolvedAt == nil {
		t.Fatalf("resolve status = %q, resolved_at=%v", res.Status, res.ResolvedAt)
	}
}

func TestAnalyze_WithDiagnoser(t *testing.T) {
	r := newTestRouterWithDiagnoser(&fakeDiagnoser{conclusion: "根因：消费者连接异常"})

	created := doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{Service: "svc", Title: "t"})
	var inc dto.IncidentResponse
	_ = json.Unmarshal(created.Body.Bytes(), &inc)

	analyzed := doRequest(t, r, http.MethodPost, "/api/v1/incidents/"+inc.ID+"/analyze", nil)
	if analyzed.Code != http.StatusOK {
		t.Fatalf("analyze status = %d", analyzed.Code)
	}
	var a dto.AnalyzeResponse
	_ = json.Unmarshal(analyzed.Body.Bytes(), &a)
	if a.Incident.Status != "INVESTIGATING" {
		t.Fatalf("incident status = %q", a.Incident.Status)
	}
	if a.Diagnosis == nil || a.Diagnosis.Conclusion != "根因：消费者连接异常" {
		t.Fatalf("diagnosis = %+v", a.Diagnosis)
	}
}

func TestResolveFromOpenIsConflict(t *testing.T) {
	r := newTestRouter()
	created := doRequest(t, r, http.MethodPost, "/api/v1/incidents", dto.CreateIncidentRequest{Service: "svc", Title: "t"})
	var inc dto.IncidentResponse
	_ = json.Unmarshal(created.Body.Bytes(), &inc)

	w := doRequest(t, r, http.MethodPost, "/api/v1/incidents/"+inc.ID+"/resolve", nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("resolve(OPEN) status = %d, want 409", w.Code)
	}
}

func TestRunEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	runRepo := repository.NewMemoryRun()
	broker := agentruntime.NewStreamBroker()

	svc := incidentservice.New(repository.NewMemory())
	incidentHandler := handler.NewIncidentHandler(svc)
	runHandler := handler.NewRunHandler(runRepo, broker)
	approvalHandler := handler.NewApprovalHandler(approval.New(repository.NewMemoryApproval(), broker, nil))
	alertHandler := handler.NewAlertHandler(svc)
	r := New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler)

	now := time.Now()
	_ = runRepo.CreateRun(context.Background(), &model.AgentRun{
		ID: "run_1", IncidentID: "inc_1", AgentType: "diagnosis",
		Status: model.RunCompleted, Goal: "t", StartedAt: now,
	})
	_ = runRepo.AddStep(context.Background(), &model.AgentStep{
		ID: "step_1", RunID: "run_1", StepIndex: 1, Agent: "diagnosis", Action: "finalize", Status: "COMPLETED",
	})

	w := doRequest(t, r, http.MethodGet, "/api/v1/runs/run_1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get run status = %d, want 200", w.Code)
	}
	var run model.AgentRun
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	if run.Status != model.RunCompleted {
		t.Fatalf("run status = %s", run.Status)
	}

	w = doRequest(t, r, http.MethodGet, "/api/v1/runs/run_1/steps", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get steps status = %d", w.Code)
	}
	var steps []model.AgentStep
	_ = json.Unmarshal(w.Body.Bytes(), &steps)
	if len(steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(steps))
	}

	w = doRequest(t, r, http.MethodGet, "/api/v1/runs/missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get missing run status = %d, want 404", w.Code)
	}
}

func TestApprovalEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	approvalRepo := repository.NewMemoryApproval()
	broker := agentruntime.NewStreamBroker()
	approvalSvc := approval.New(approvalRepo, broker, nil)

	svc := incidentservice.New(repository.NewMemory())
	incidentHandler := handler.NewIncidentHandler(svc)
	runHandler := handler.NewRunHandler(repository.NewMemoryRun(), broker)
	approvalHandler := handler.NewApprovalHandler(approvalSvc)
	alertHandler := handler.NewAlertHandler(svc)
	r := New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler)

	a, err := approvalSvc.Request(context.Background(), "run_1", "call_1", "restart_worker", "{\"target\":\"worker\"}", "consumer down")
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}

	w := doRequest(t, r, http.MethodGet, "/api/v1/approvals/"+a.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get approval status = %d", w.Code)
	}

	w = doRequest(t, r, http.MethodPost, "/api/v1/approvals/"+a.ID+"/approve", dto.ApprovalActionRequest{ApprovedBy: "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("approve status = %d", w.Code)
	}
	var approved model.Approval
	_ = json.Unmarshal(w.Body.Bytes(), &approved)
	if approved.Status != model.ApprovalApproved {
		t.Fatalf("status = %s, want APPROVED", approved.Status)
	}

	w = doRequest(t, r, http.MethodPost, "/api/v1/approvals/"+a.ID+"/approve", dto.ApprovalActionRequest{ApprovedBy: "admin"})
	if w.Code != http.StatusConflict {
		t.Fatalf("second approve status = %d, want 409", w.Code)
	}

	w = doRequest(t, r, http.MethodGet, "/api/v1/approvals/missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get missing approval status = %d, want 404", w.Code)
	}
}

func TestAlertWebhook(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := incidentservice.New(repository.NewMemory())
	incidentHandler := handler.NewIncidentHandler(svc)
	runHandler := handler.NewRunHandler(repository.NewMemoryRun(), agentruntime.NewStreamBroker())
	approvalHandler := handler.NewApprovalHandler(approval.New(repository.NewMemoryApproval(), agentruntime.NewStreamBroker(), nil))
	alertHandler := handler.NewAlertHandler(svc)
	r := New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler)

	webhook := dto.AlertWebhook{
		Status: "firing",
		Alerts: []dto.Alert{{
			Status:      "firing",
			Labels:      map[string]string{"alertname": "HighQueueDepth", "service": "gocommunity", "severity": "P1"},
			Annotations: map[string]string{"summary": "rabbitmq backlog", "description": "queue depth high"},
		}},
	}

	w := doRequest(t, r, http.MethodPost, "/api/v1/alerts", webhook)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook status = %d", w.Code)
	}
	var resp dto.AlertWebhookResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Created) != 1 {
		t.Fatalf("created = %v, want 1", resp.Created)
	}

	// 去重：同告警再发，不应新建
	w = doRequest(t, r, http.MethodPost, "/api/v1/alerts", webhook)
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Created) != 0 {
		t.Fatalf("dedup created = %v, want 0", resp.Created)
	}

	// 告警恢复：关闭 Incident
	resolvedWebhook := dto.AlertWebhook{
		Status: "resolved",
		Alerts: []dto.Alert{{
			Status:      "resolved",
			Labels:      map[string]string{"alertname": "HighQueueDepth", "service": "gocommunity"},
			Annotations: map[string]string{"summary": "rabbitmq backlog"},
		}},
	}
	w = doRequest(t, r, http.MethodPost, "/api/v1/alerts", resolvedWebhook)
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Resolved) != 1 {
		t.Fatalf("resolved = %v, want 1", resp.Resolved)
	}

	list, _ := svc.List(context.Background(), repository.ListFilter{})
	if len(list) != 1 || list[0].Status != model.StatusResolved {
		t.Fatalf("incident list = %+v", list)
	}
}
