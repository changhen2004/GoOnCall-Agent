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
	"gooncall-agent/internal/api/middleware"
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
	return New(handler.NewIncidentHandler(svc), handler.NewHealthHandler(), newRunHandler(), newApprovalHandler(), newAlertHandler(), middleware.AuthConfig{})
}

func newTestRouterWithDiagnoser(d handler.Diagnoser) *gin.Engine {
	gin.SetMode(gin.TestMode)
	svc := incidentservice.New(repository.NewMemory())
	incidentHandler := handler.NewIncidentHandler(svc).WithDiagnoser(d, "你是诊断 agent")
	return New(incidentHandler, handler.NewHealthHandler(), newRunHandler(), newApprovalHandler(), newAlertHandler(), middleware.AuthConfig{})
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

	// 严格状态机：INVESTIGATING 直接 RESOLVED 被禁止 -> 409
	resolved := doRequest(t, r, http.MethodPost, "/api/v1/incidents/"+inc.ID+"/resolve", nil)
	if resolved.Code != http.StatusConflict {
		t.Fatalf("resolve(INVESTIGATING) status = %d, want 409", resolved.Code)
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
	r := New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler, middleware.AuthConfig{})

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
	r := New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler, middleware.AuthConfig{})

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
	svc := incidentservice.New(repository.NewMemory())
	incidentHandler := handler.NewIncidentHandler(svc)
	runHandler := handler.NewRunHandler(repository.NewMemoryRun(), agentruntime.NewStreamBroker())
	approvalHandler := handler.NewApprovalHandler(approval.New(repository.NewMemoryApproval(), agentruntime.NewStreamBroker(), nil))
	alertHandler := handler.NewAlertHandler(svc)
	r := New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler, middleware.AuthConfig{})

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

// queryEnv 是查询 API 测试用的装配（全部复用已有 Memory 仓库）。
type queryEnv struct {
	router       *gin.Engine
	incidentSvc  *incidentservice.Service
	runRepo      *repository.MemoryRun
	approvalSvc  *approval.Service
	approvalRepo *repository.MemoryApproval
}

func newQueryEnv(t *testing.T) *queryEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	incidentRepo := repository.NewMemory()
	runRepo := repository.NewMemoryRun()
	approvalRepo := repository.NewMemoryApproval()
	broker := agentruntime.NewStreamBroker()

	incidentSvc := incidentservice.New(incidentRepo)
	approvalSvc := approval.New(approvalRepo, broker, nil).WithIncidentState(incidentSvc, runRepo)

	incidentHandler := handler.NewIncidentHandler(incidentSvc)
	runHandler := handler.NewRunHandler(runRepo, broker).WithIncidentRepository(incidentRepo)
	approvalHandler := handler.NewApprovalHandler(approvalSvc)
	alertHandler := handler.NewAlertHandler(incidentSvc)

	return &queryEnv{
		router:       New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler, middleware.AuthConfig{}),
		incidentSvc:  incidentSvc,
		runRepo:      runRepo,
		approvalSvc:  approvalSvc,
		approvalRepo: approvalRepo,
	}
}

// seedApproval 直接写一条审批记录，用于验证查询顺序与状态过滤。
func (e *queryEnv) seedApproval(t *testing.T, id, runID, toolCallID string, status model.ApprovalStatus, createdAt time.Time) *model.Approval {
	t.Helper()
	a := &model.Approval{
		ID: id, RunID: runID, ToolCallID: toolCallID, Action: "restart_worker",
		Arguments: `{"target":"w"}`, Status: status,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	if err := e.approvalRepo.Create(context.Background(), a); err != nil {
		t.Fatalf("seed approval %s: %v", id, err)
	}
	return a
}

// TestQueryIncidentRuns 验证 /incidents/:id/runs 的倒序、空集合与父资源 404。
func TestQueryIncidentRuns(t *testing.T) {
	env := newQueryEnv(t)
	ctx := context.Background()

	inc, _, err := env.incidentSvc.Create(ctx, incidentservice.CreateIncidentInput{Service: "svc", Title: "t", AlertName: "a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	now := time.Now()
	if err := env.runRepo.CreateRun(ctx, &model.AgentRun{
		ID: "run_old", IncidentID: inc.ID, AgentType: "diagnosis",
		Status: model.RunFailed, StartedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("CreateRun(old) error = %v", err)
	}
	if err := env.runRepo.CreateRun(ctx, &model.AgentRun{
		ID: "run_new", IncidentID: inc.ID, AgentType: "diagnosis",
		Status: model.RunRunning, StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun(new) error = %v", err)
	}

	w := doRequest(t, env.router, http.MethodGet, "/api/v1/incidents/"+inc.ID+"/runs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var runs []model.AgentRun
	if err := json.Unmarshal(w.Body.Bytes(), &runs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != "run_new" || runs[1].ID != "run_old" {
		t.Fatalf("runs = %+v, want [run_new run_old]", runs)
	}

	// 空集合：必须是 []，不是 null。
	other, _, _ := env.incidentSvc.Create(ctx, incidentservice.CreateIncidentInput{Service: "svc", Title: "other"})
	w = doRequest(t, env.router, http.MethodGet, "/api/v1/incidents/"+other.ID+"/runs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("empty status = %d", w.Code)
	}
	if got := string(bytes.TrimSpace(w.Body.Bytes())); got != "[]" {
		t.Fatalf("empty runs body = %s, want []", got)
	}

	// 父资源不存在 -> 404。
	w = doRequest(t, env.router, http.MethodGet, "/api/v1/incidents/missing/runs", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing incident status = %d, want 404", w.Code)
	}
}

// TestQueryRunToolCalls 验证 /runs/:id/tool-calls 的正序、空集合与父资源 404。
func TestQueryRunToolCalls(t *testing.T) {
	env := newQueryEnv(t)
	ctx := context.Background()

	now := time.Now()
	if err := env.runRepo.CreateRun(ctx, &model.AgentRun{
		ID: "run_1", IncidentID: "inc_1", AgentType: "diagnosis", Status: model.RunRunning, StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	for i, id := range []string{"call_1", "call_2"} {
		if err := env.runRepo.AddToolCall(ctx, &model.ToolCall{
			ID: id, RunID: "run_1", ToolName: "runbook_search", RiskLevel: "LOW",
			Status: "COMPLETED", CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("AddToolCall(%s) error = %v", id, err)
		}
	}

	w := doRequest(t, env.router, http.MethodGet, "/api/v1/runs/run_1/tool-calls", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var calls []model.ToolCall
	if err := json.Unmarshal(w.Body.Bytes(), &calls); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(calls) != 2 || calls[0].ID != "call_1" || calls[1].ID != "call_2" {
		t.Fatalf("calls = %+v, want ascending order", calls)
	}

	// 空集合
	if err := env.runRepo.CreateRun(ctx, &model.AgentRun{
		ID: "run_empty", IncidentID: "inc_2", AgentType: "diagnosis", Status: model.RunRunning, StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun(empty) error = %v", err)
	}
	w = doRequest(t, env.router, http.MethodGet, "/api/v1/runs/run_empty/tool-calls", nil)
	if got := string(bytes.TrimSpace(w.Body.Bytes())); got != "[]" {
		t.Fatalf("empty tool calls body = %s, want []", got)
	}

	// 父资源不存在 -> 404
	w = doRequest(t, env.router, http.MethodGet, "/api/v1/runs/missing/tool-calls", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing run status = %d, want 404", w.Code)
	}
}

// TestQueryRunApprovals 验证 /runs/:id/approvals 的正序、空集合与父资源 404。
func TestQueryRunApprovals(t *testing.T) {
	env := newQueryEnv(t)
	ctx := context.Background()

	now := time.Now()
	if err := env.runRepo.CreateRun(ctx, &model.AgentRun{
		ID: "run_1", IncidentID: "inc_1", AgentType: "diagnosis", Status: model.RunRunning, StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	// 空集合：必须是 []，不是 null。
	w := doRequest(t, env.router, http.MethodGet, "/api/v1/runs/run_1/approvals", nil)
	if w.Code != http.StatusOK || string(bytes.TrimSpace(w.Body.Bytes())) != "[]" {
		t.Fatalf("empty approvals = %d %s, want 200 []", w.Code, w.Body.String())
	}

	first := env.seedApproval(t, "ap_1", "run_1", "call_1", model.ApprovalPending, now)
	second := env.seedApproval(t, "ap_2", "run_1", "call_2", model.ApprovalPending, now.Add(time.Second))

	w = doRequest(t, env.router, http.MethodGet, "/api/v1/runs/run_1/approvals", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var items []model.Approval
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 2 || items[0].ID != first.ID || items[1].ID != second.ID {
		t.Fatalf("approvals = %+v, want ascending by created_at", items)
	}
	if items[0].ToolCallID != "call_1" {
		t.Fatalf("tool_call_id = %q, want call_1 (approval 与 tool call 关联可查)", items[0].ToolCallID)
	}

	// 父资源不存在 -> 404
	w = doRequest(t, env.router, http.MethodGet, "/api/v1/runs/missing/approvals", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing run status = %d, want 404", w.Code)
	}
}

// TestQueryPendingApprovals 验证 /approvals?status=PENDING。
func TestQueryPendingApprovals(t *testing.T) {
	env := newQueryEnv(t)
	now := time.Now()

	w := doRequest(t, env.router, http.MethodGet, "/api/v1/approvals?status=PENDING", nil)
	if w.Code != http.StatusOK || string(bytes.TrimSpace(w.Body.Bytes())) != "[]" {
		t.Fatalf("empty pending = %d %s, want 200 []", w.Code, w.Body.String())
	}

	pending := env.seedApproval(t, "ap_pending", "run_1", "call_1", model.ApprovalPending, now)
	env.seedApproval(t, "ap_rejected", "run_1", "call_2", model.ApprovalRejected, now.Add(time.Second))

	w = doRequest(t, env.router, http.MethodGet, "/api/v1/approvals?status=PENDING", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var items []model.Approval
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(items) != 1 || items[0].ID != pending.ID {
		t.Fatalf("pending = %+v, want only %s", items, pending.ID)
	}

	// 不支持的过滤条件 -> 400（不假装支持其它状态）。
	w = doRequest(t, env.router, http.MethodGet, "/api/v1/approvals?status=EXECUTED", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unsupported status = %d, want 400", w.Code)
	}
}

// TestQueryIncidentEvents 验证 /incidents/:id/events 的正序与父资源 404。
func TestQueryIncidentEvents(t *testing.T) {
	env := newQueryEnv(t)
	ctx := context.Background()

	inc, _, err := env.incidentSvc.Create(ctx, incidentservice.CreateIncidentInput{Service: "svc", Title: "t", AlertName: "a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := env.incidentSvc.Analyze(ctx, inc.ID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	w := doRequest(t, env.router, http.MethodGet, "/api/v1/incidents/"+inc.ID+"/events", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var events []model.IncidentEvent
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want 2 (created + transition)", events)
	}
	if events[0].EventType != model.EventIncidentCreated || events[1].EventType != model.EventStatusTransition {
		t.Fatalf("event order/types = %+v", events)
	}
	from, to := events[1].StatusChange()
	if from != model.StatusOpen || to != model.StatusInvestigating {
		t.Fatalf("transition = %s -> %s, want OPEN -> INVESTIGATING", from, to)
	}

	// 父资源不存在 -> 404
	w = doRequest(t, env.router, http.MethodGet, "/api/v1/incidents/missing/events", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing incident events status = %d, want 404", w.Code)
	}
}

const (
	testReadToken    = "read-token-def"
	testAdminToken   = "admin-token-ghi"
	testWebhookToken = "webhook-token-abc"
	testAdminActor   = "alice@ops"
)

type authEnv struct {
	router       *gin.Engine
	approvalRepo *repository.MemoryApproval
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	incidentRepo := repository.NewMemory()
	runRepo := repository.NewMemoryRun()
	approvalRepo := repository.NewMemoryApproval()
	broker := agentruntime.NewStreamBroker()

	incidentSvc := incidentservice.New(incidentRepo)
	// 认证用例只覆盖中间件与 handler：不装配 Incident 状态闭环与执行器。
	approvalSvc := approval.New(approvalRepo, broker, nil)

	incidentHandler := handler.NewIncidentHandler(incidentSvc)
	runHandler := handler.NewRunHandler(runRepo, broker).WithIncidentRepository(incidentRepo)
	approvalHandler := handler.NewApprovalHandler(approvalSvc)
	alertHandler := handler.NewAlertHandler(incidentSvc)

	r := New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler, middleware.AuthConfig{
		WebhookToken: testWebhookToken,
		ReadToken:    testReadToken,
		AdminToken:   testAdminToken,
		AdminActor:   testAdminActor,
	})
	return &authEnv{router: r, approvalRepo: approvalRepo}
}

func authRequest(t *testing.T, r *gin.Engine, method, path, bearer string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestAuth_ReadAndAdminTokens 覆盖无 / 错 Token 的 401、read 与 admin 的读权限。
func TestAuth_ReadAndAdminTokens(t *testing.T) {
	env := newAuthEnv(t)

	if w := authRequest(t, env.router, http.MethodGet, "/api/v1/incidents", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodGet, "/api/v1/incidents", "wrong", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodGet, "/api/v1/incidents", testWebhookToken, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("webhook token on read route = %d, want 401", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodGet, "/api/v1/incidents", testReadToken, nil); w.Code != http.StatusOK {
		t.Fatalf("read token = %d, want 200", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodGet, "/api/v1/incidents", testAdminToken, nil); w.Code != http.StatusOK {
		t.Fatalf("admin token on read route = %d, want 200 (admin 具备读权限)", w.Code)
	}
}

func TestAuth_IncidentWritesRequireAdmin(t *testing.T) {
	env := newAuthEnv(t)
	body := dto.CreateIncidentRequest{Service: "svc", Title: "queue backlog"}

	if w := authRequest(t, env.router, http.MethodPost, "/api/v1/incidents", testReadToken, body); w.Code != http.StatusForbidden {
		t.Fatalf("read token create incident = %d, want 403", w.Code)
	}
	createdW := authRequest(t, env.router, http.MethodPost, "/api/v1/incidents", testAdminToken, body)
	if createdW.Code != http.StatusCreated {
		t.Fatalf("admin token create incident = %d, want 201, body=%s", createdW.Code, createdW.Body.String())
	}
	var inc model.Incident
	if err := json.Unmarshal(createdW.Body.Bytes(), &inc); err != nil {
		t.Fatalf("decode created incident: %v", err)
	}
	if w := authRequest(t, env.router, http.MethodPost, "/api/v1/incidents/"+inc.ID+"/analyze", testReadToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("read token analyze incident = %d, want 403", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodPost, "/api/v1/incidents/"+inc.ID+"/resolve", testReadToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("read token resolve incident = %d, want 403", w.Code)
	}
}

// TestAuth_ApproveRequiresAdmin 批准 / 拒绝只接受 admin token；read token 返回 403。
func TestAuth_ApproveRequiresAdmin(t *testing.T) {
	env := newAuthEnv(t)
	ctx := context.Background()
	if err := env.approvalRepo.Create(ctx, &model.Approval{
		ID: "ap_1", IncidentID: "inc_1", RunID: "run_1", ToolCallID: "call_1",
		Action: "restart_worker", Arguments: `{"target":"w"}`, Status: model.ApprovalPending,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed approval: %v", err)
	}

	if w := authRequest(t, env.router, http.MethodPost, "/api/v1/approvals/ap_1/approve", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated approve = %d, want 401", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodPost, "/api/v1/approvals/ap_1/approve", testReadToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("read token approve = %d, want 403", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodPost, "/api/v1/approvals/ap_1/approve", testWebhookToken, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("webhook token approve = %d, want 401", w.Code)
	}
}

// TestAuth_ForgedApprovedByIgnored 请求体里的 approved_by 无效，落库值来自服务端配置。
func TestAuth_ForgedApprovedByIgnored(t *testing.T) {
	env := newAuthEnv(t)
	ctx := context.Background()
	if err := env.approvalRepo.Create(ctx, &model.Approval{
		ID: "ap_forged", IncidentID: "inc_1", RunID: "run_1", ToolCallID: "call_1",
		Action: "restart_worker", Arguments: `{"target":"w"}`, Status: model.ApprovalPending,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed approval: %v", err)
	}

	w := authRequest(t, env.router, http.MethodPost, "/api/v1/approvals/ap_forged/approve", testAdminToken,
		dto.ApprovalActionRequest{ApprovedBy: "attacker"})
	if w.Code != http.StatusOK {
		t.Fatalf("approve = %d, body=%s", w.Code, w.Body.String())
	}

	stored, err := env.approvalRepo.Get(ctx, "ap_forged")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.ApprovedBy != testAdminActor {
		t.Fatalf("ApprovedBy = %q, want the server-configured actor %q (请求体必须被忽略)", stored.ApprovedBy, testAdminActor)
	}
}

// TestAuth_WebhookTokenRequired webhook 只接受 X-GoOnCall-Webhook-Token。
func TestAuth_WebhookTokenRequired(t *testing.T) {
	env := newAuthEnv(t)
	webhook := dto.AlertWebhook{Status: "firing", Alerts: []dto.Alert{{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "HighQueueDepth", "service": "svc"},
		Annotations: map[string]string{"summary": "backlog"},
	}}}

	if w := doRequest(t, env.router, http.MethodPost, "/api/v1/alerts", webhook); w.Code != http.StatusUnauthorized {
		t.Fatalf("webhook without token = %d, want 401", w.Code)
	}
	if w := authRequest(t, env.router, http.MethodPost, "/api/v1/alerts", testReadToken, webhook); w.Code != http.StatusUnauthorized {
		t.Fatalf("webhook with bearer token = %d, want 401", w.Code)
	}

	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(webhook)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GoOnCall-Webhook-Token", testWebhookToken)
	w := httptest.NewRecorder()
	env.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("webhook with webhook token = %d, want 200, body=%s", w.Code, w.Body.String())
	}
}

// TestAuth_DisabledWhenNoTokensConfigured 显式 Demo（无 Token）不启用认证。
func TestAuth_DisabledWhenNoTokensConfigured(t *testing.T) {
	r := newTestRouter()
	if w := doRequest(t, r, http.MethodGet, "/api/v1/incidents", nil); w.Code != http.StatusOK {
		t.Fatalf("demo read = %d, want 200", w.Code)
	}
}

// TestAuth_PartialConfigFailsClosed 只配置部分 Token 时，其余权限组不能退化为无认证。
func TestAuth_PartialConfigFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	incidentSvc := incidentservice.New(repository.NewMemory())
	r := New(
		handler.NewIncidentHandler(incidentSvc),
		handler.NewHealthHandler(),
		handler.NewRunHandler(repository.NewMemoryRun(), agentruntime.NewStreamBroker()),
		handler.NewApprovalHandler(approval.New(repository.NewMemoryApproval(), agentruntime.NewStreamBroker(), nil)),
		handler.NewAlertHandler(incidentSvc),
		middleware.AuthConfig{WebhookToken: testWebhookToken},
	)

	if w := doRequest(t, r, http.MethodGet, "/api/v1/incidents", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("read route with partial auth = %d, want 401", w.Code)
	}
	if w := doRequest(t, r, http.MethodPost, "/api/v1/approvals/ap_1/approve", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("admin route with partial auth = %d, want 401", w.Code)
	}
}
