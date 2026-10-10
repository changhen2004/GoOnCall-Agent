// Package integration 提供闭环装配级集成测试。
// 这里装配真实 HTTP 路由、认证中间件、HTTP 处置适配器和 Prometheus 采集器
// （后两者使用本地 httptest stub），并用 HTTP 请求驱动完整链路。
// 不依赖 docker、外部服务或真实生产凭证。
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/api/middleware"
	"gooncall-agent/internal/api/router"
	"gooncall-agent/internal/bootstrap"
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

const (
	webhookToken = "webhook-token-abc"
	readToken    = "read-token-def"
	adminToken   = "admin-token-ghi"
	adminActor   = "alice@ops"
	restartToken = "restart-token-secret"
	target       = "resource-community-worker"
)

// restartStub 是固定处置 endpoint 的本地替身：记录调用次数与幂等键。
type restartStub struct {
	mu       sync.Mutex
	calls    int
	idemKeys []string
	authSeen []string
	server   *httptest.Server
}

func newRestartStub(t *testing.T) *restartStub {
	t.Helper()
	// 故意回显 Authorization 到响应体：适配器不得把 Token 带进错误或结果。
	stub := &restartStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.calls++
		stub.idemKeys = append(stub.idemKeys, r.Header.Get("Idempotency-Key"))
		stub.authSeen = append(stub.authSeen, r.Header.Get("Authorization"))
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"operation_id":"op-42"}`))
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *restartStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *restartStub) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.idemKeys...)
}

// prometheusStub 返回"已恢复"指标。
func newPrometheusStub(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := "0.001"
		switch r.URL.Query().Get("query") {
		case verifier.DefaultQueries().Consumers:
			value = "5"
		case verifier.DefaultQueries().QueueDepth:
			value = "50"
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,` + value + `]}]}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// fakeModel 脚本化 LLM：证据充分后调用 worker_restart 创建审批。
type fakeModel struct{}

func (f *fakeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, m := range input {
		if m.Role == schema.Tool {
			return &schema.Message{Role: schema.Assistant, Content: "根因：消费者连接异常。已提交 worker_restart 审批。"}, nil
		}
	}
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{ID: "call_1", Type: "function", Function: schema.FunctionCall{
				Name: "worker_restart", Arguments: `{"target":"` + target + `","reason":"consumer count dropped"}`,
			}},
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

// inlinePublisher 把 agent.requested 直接交给 Worker 的幂等入口处理（不经过 RabbitMQ）。
type inlinePublisher struct {
	mu       sync.Mutex
	delivery int
	svc      *incidentservice.Service
	runRepo  repository.RunRepository
	rt       *agentruntime.Runtime
}

func (p *inlinePublisher) PublishAgentRequested(ctx context.Context, incidentID string) error {
	ev, err := newAgentRequestedEvent(incidentID)
	if err != nil {
		return err
	}
	return p.deliver(ctx, ev)
}

// deliver 模拟 Worker 消费一条事件（同一事件可重复投递，用于验证幂等）。
func (p *inlinePublisher) deliver(ctx context.Context, ev agentEvent) error {
	p.mu.Lock()
	p.delivery++
	p.mu.Unlock()
	return bootstrap.RunDiagnosis(ctx, p.svc, p.runRepo, p.rt, "你是诊断 agent", ev.IncidentID)
}

type app struct {
	engine       *gin.Engine
	incidentSvc  *incidentservice.Service
	runRepo      *repository.MemoryRun
	approvalRepo *repository.MemoryApproval
	approvalSvc  *approval.Service
	publisher    *inlinePublisher
	restart      *restartStub
	events       []agentEvent
}

func setup(t *testing.T) *app {
	t.Helper()
	gin.SetMode(gin.TestMode)

	incidentRepo := repository.NewMemory()
	runRepo := repository.NewMemoryRun()
	approvalRepo := repository.NewMemoryApproval()
	broker := agentruntime.NewStreamBroker()
	incidentSvc := incidentservice.New(incidentRepo)

	// 工具注册表：只读检索 + 真实处置工具（HTTP 适配器 + target 白名单）。
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "rabbitmq.md"), []byte("# 消费者异常\n\n检查 consumer count。\n"), 0o600)
	reg := registry.New()
	if rb, err := runbook.New(dir).EinoTool(); err == nil {
		_ = reg.Register(rb, registry.RiskLow)
	}

	restart := newRestartStub(t)
	restartTool := deployment.New(nil, policy.New(true))
	httpExec, err := deployment.NewHTTPExecutor(deployment.HTTPExecutorConfig{
		URL: restart.server.URL, Token: restartToken, Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewHTTPExecutor() error = %v", err)
	}
	restartTool.WithHTTPExecutor(httpExec).WithAllowedTargets([]string{target})
	if eino, err := restartTool.EinoTool(); err == nil {
		_ = reg.Register(eino, registry.RiskMedium)
	}

	// 真实 Prometheus 采集器指向本地 stub；连续 3 次通过才允许 RESOLVED。
	gatherer := verifier.NewPrometheusGatherer(newPrometheusStub(t).URL, verifier.DefaultQueries())
	remediation := executor.NewRemediation(restartTool, verifier.New(verifier.DefaultConfig()), gatherer,
		incidentSvc, runRepo, postmortem.New(), broker).
		WithVerifyConfig(verifier.RunConfig{Attempts: 3, RequiredSuccesses: 3})

	approvalSvc := approval.New(approvalRepo, broker, remediation).WithIncidentState(incidentSvc, runRepo)
	restartTool.SetApproval(approvalSvc)
	// 外部恢复顺序：先过期待审批，再关闭 Incident。
	incidentSvc.WithApprovalExpirer(approvalSvc)

	rt := agentruntime.New(&fakeModel{}, reg, 10).WithRunRecording(runRepo, broker)
	publisher := &inlinePublisher{svc: incidentSvc, runRepo: runRepo, rt: rt}

	incidentHandler := handler.NewIncidentHandler(incidentSvc).WithPublisher(publisher)
	runHandler := handler.NewRunHandler(runRepo, broker).WithIncidentRepository(incidentRepo)
	approvalHandler := handler.NewApprovalHandler(approvalSvc)
	alertHandler := handler.NewAlertHandler(incidentSvc).WithPublisher(publisher).WithRunRepository(runRepo)

	engine := router.New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler,
		middleware.AuthConfig{
			WebhookToken: webhookToken,
			ReadToken:    readToken,
			AdminToken:   adminToken,
			AdminActor:   adminActor,
		})

	return &app{
		engine: engine, incidentSvc: incidentSvc, runRepo: runRepo,
		approvalRepo: approvalRepo, approvalSvc: approvalSvc,
		publisher: publisher, restart: restart,
	}
}

type agentEvent struct {
	IncidentID string
}

func newAgentRequestedEvent(incidentID string) (agentEvent, error) {
	if incidentID == "" {
		return agentEvent{}, errors.New("incident id is required")
	}
	return agentEvent{IncidentID: incidentID}, nil
}

// do 发送一个请求并返回响应。
func (a *app) do(t *testing.T, method, path, bearer, webhook string, body any) *httptest.ResponseRecorder {
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
	if webhook != "" {
		req.Header.Set("X-GoOnCall-Webhook-Token", webhook)
	}
	w := httptest.NewRecorder()
	a.engine.ServeHTTP(w, req)
	return w
}

func firingWebhook(summary string) dto.AlertWebhook {
	return dto.AlertWebhook{
		Status: "firing",
		Alerts: []dto.Alert{{
			Status:      "firing",
			Labels:      map[string]string{"alertname": "HighQueueDepth", "service": "gocommunity", "severity": "HIGH"},
			Annotations: map[string]string{"summary": summary},
		}},
	}
}

func resolvedWebhook(summary string) dto.AlertWebhook {
	wh := firingWebhook(summary)
	wh.Status = "resolved"
	wh.Alerts[0].Status = "resolved"
	return wh
}

func decodePending(t *testing.T, body []byte) []incidentmodel.Approval {
	t.Helper()
	var items []incidentmodel.Approval
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode approvals %q: %v", body, err)
	}
	return items
}

// TestClosedLoop_HTTPExecutionAndPrometheus 覆盖真实 HTTP 处置 + Prometheus 连续验证的完整成功链路，
// 以及重复 webhook / 重复 event / 重复 approve 都只执行一次。
func TestClosedLoop_HTTPExecutionAndPrometheus(t *testing.T) {
	ctx := context.Background()
	a := setup(t)

	// 1. 一次告警：webhook -> Incident -> （Worker）诊断 -> Agent 自动创建审批。
	w := a.do(t, http.MethodPost, "/api/v1/alerts", "", webhookToken, firingWebhook("rabbitmq backlog"))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook = %d, body=%s", w.Code, w.Body.String())
	}
	var whResp dto.AlertWebhookResponse
	_ = json.Unmarshal(w.Body.Bytes(), &whResp)
	if len(whResp.Created) != 1 {
		t.Fatalf("created = %v, want 1", whResp.Created)
	}
	incidentID := whResp.Created[0]

	runs, _ := a.runRepo.ListRunsByIncident(ctx, incidentID)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (Agent 自动创建审批需要一次诊断)", len(runs))
	}
	pending := decodePending(t, a.do(t, http.MethodGet, "/api/v1/approvals?status=PENDING", readToken, "", nil).Body.Bytes())
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %+v, want 1 (由 Agent 工具创建，非测试手工调用)", pending)
	}
	ap := pending[0]
	calls, err := a.runRepo.ListToolCalls(ctx, runs[0].ID)
	if err != nil {
		t.Fatalf("ListToolCalls() error = %v", err)
	}
	var restartCall *incidentmodel.ToolCall
	for _, call := range calls {
		if call.ToolName == "worker_restart" {
			restartCall = call
			break
		}
	}
	if restartCall == nil {
		t.Fatalf("tool calls = %+v, want worker_restart", calls)
	}
	if ap.ToolCallID == "" || ap.ToolCallID != restartCall.ID {
		t.Fatalf("approval.tool_call_id = %q, worker_restart.id = %q, want equal", ap.ToolCallID, restartCall.ID)
	}

	// 2. 重复 webhook：不再新建 Incident，也不再触发新的诊断。
	w = a.do(t, http.MethodPost, "/api/v1/alerts", "", webhookToken, firingWebhook("rabbitmq backlog"))
	_ = json.Unmarshal(w.Body.Bytes(), &whResp)
	if len(whResp.Created) != 0 {
		t.Fatalf("duplicate webhook created = %v, want 0", whResp.Created)
	}
	if runs, _ := a.runRepo.ListRunsByIncident(ctx, incidentID); len(runs) != 1 {
		t.Fatalf("runs after duplicate webhook = %d, want 1", len(runs))
	}

	// 3. 重复 event（重投同一消息）：幂等入口直接成功确认，不产生第二个 Run。
	ev, _ := newAgentRequestedEvent(incidentID)
	for i := 0; i < 2; i++ {
		if err := a.publisher.deliver(ctx, ev); err != nil {
			t.Fatalf("duplicate event delivery %d error = %v", i, err)
		}
	}
	if runs, _ := a.runRepo.ListRunsByIncident(ctx, incidentID); len(runs) != 1 {
		t.Fatalf("runs after duplicate events = %d, want 1", len(runs))
	}

	// 4. 未认证 / 权限不足的批准被拒绝。
	if w := a.do(t, http.MethodPost, "/api/v1/approvals/"+ap.ID+"/approve", "", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated approve = %d, want 401", w.Code)
	}
	if w := a.do(t, http.MethodPost, "/api/v1/approvals/"+ap.ID+"/approve", readToken, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("read-token approve = %d, want 403", w.Code)
	}

	// 5. admin 批准：请求体伪造的 approved_by 无效，处置只调用一次且幂等键正确。
	w = a.do(t, http.MethodPost, "/api/v1/approvals/"+ap.ID+"/approve", adminToken, "",
		dto.ApprovalActionRequest{ApprovedBy: "attacker"})
	if w.Code != http.StatusOK {
		t.Fatalf("approve = %d, body=%s", w.Code, w.Body.String())
	}
	if got := a.restart.count(); got != 1 {
		t.Fatalf("restart endpoint calls = %d, want 1", got)
	}
	if keys := a.restart.keys(); len(keys) != 1 || keys[0] != ap.ID {
		t.Fatalf("idempotency keys = %v, want [%s]", keys, ap.ID)
	}
	stored, err := a.approvalSvc.Get(ctx, ap.ID)
	if err != nil {
		t.Fatalf("Get(approval) error = %v", err)
	}
	if stored.ApprovedBy != adminActor {
		t.Fatalf("ApprovedBy = %q, want configured actor %q", stored.ApprovedBy, adminActor)
	}
	if stored.OperationID != "op-42" {
		t.Fatalf("OperationID = %q, want op-42", stored.OperationID)
	}
	if strings.Contains(stored.Result, restartToken) {
		t.Fatalf("approval result leaks the restart token: %s", stored.Result)
	}

	// 6. 重复批准：conflict，且不会产生第二次真实处置。
	if w := a.do(t, http.MethodPost, "/api/v1/approvals/"+ap.ID+"/approve", adminToken, "", nil); w.Code != http.StatusConflict {
		t.Fatalf("second approve = %d, want 409", w.Code)
	}
	if got := a.restart.count(); got != 1 {
		t.Fatalf("restart endpoint calls after duplicate approve = %d, want 1", got)
	}

	// 7. 连续验证通过后 RESOLVED，事件与复盘可查询。
	inc, err := a.incidentSvc.Get(ctx, incidentID)
	if err != nil {
		t.Fatalf("Get(incident) error = %v", err)
	}
	if inc.Status != incidentmodel.StatusResolved {
		t.Fatalf("incident status = %s, want RESOLVED", inc.Status)
	}
	if !strings.Contains(stored.Result, "## Prevention") {
		t.Fatalf("approval result should carry the postmortem: %s", stored.Result)
	}

	w = a.do(t, http.MethodGet, "/api/v1/incidents/"+incidentID+"/events", readToken, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("events = %d, body=%s", w.Code, w.Body.String())
	}
	var events []incidentmodel.IncidentEvent
	if err := json.Unmarshal(w.Body.Bytes(), &events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	last := events[len(events)-1]
	if _, to := last.StatusChange(); to != incidentmodel.StatusResolved {
		t.Fatalf("last event = %+v, want the RESOLVED transition", last)
	}
	var postmortemEvent *incidentmodel.IncidentEvent
	for _, event := range events {
		if event.EventType == incidentmodel.EventPostmortemGenerated {
			eventCopy := event
			postmortemEvent = &eventCopy
			break
		}
	}
	if postmortemEvent == nil {
		t.Fatalf("events = %+v, want POSTMORTEM_GENERATED", events)
	}
	if postmortemEvent.Payload["approval_id"] != ap.ID {
		t.Fatalf("postmortem approval_id = %v, want %s", postmortemEvent.Payload["approval_id"], ap.ID)
	}
	if generated, _ := postmortemEvent.Payload["postmortem"].(string); !strings.Contains(generated, "## Prevention") {
		t.Fatalf("postmortem event payload = %+v", postmortemEvent.Payload)
	}
}

// TestClosedLoop_ExternalRecoveryExpiresPendingApproval 外部恢复后旧审批不能再执行。
func TestClosedLoop_ExternalRecoveryExpiresPendingApproval(t *testing.T) {
	ctx := context.Background()
	a := setup(t)

	// 先产生一个待审批（诊断链路自动创建）。
	w := a.do(t, http.MethodPost, "/api/v1/alerts", "", webhookToken, firingWebhook("external recovery"))
	if w.Code != http.StatusOK {
		t.Fatalf("webhook = %d, body=%s", w.Code, w.Body.String())
	}
	var whResp dto.AlertWebhookResponse
	_ = json.Unmarshal(w.Body.Bytes(), &whResp)
	if len(whResp.Created) != 1 {
		t.Fatalf("created = %v, want 1", whResp.Created)
	}
	incidentID := whResp.Created[0]

	pending := decodePending(t, a.do(t, http.MethodGet, "/api/v1/approvals?status=PENDING", readToken, "", nil).Body.Bytes())
	if len(pending) != 1 {
		t.Fatalf("pending approvals = %+v, want 1", pending)
	}
	ap := pending[0]

	// 外部恢复：先让待审批失效，再关闭 Incident。
	w = a.do(t, http.MethodPost, "/api/v1/alerts", "", webhookToken, resolvedWebhook("external recovery"))
	if w.Code != http.StatusOK {
		t.Fatalf("resolved webhook = %d, body=%s", w.Code, w.Body.String())
	}
	expired, err := a.approvalSvc.Get(ctx, ap.ID)
	if err != nil {
		t.Fatalf("Get(approval) error = %v", err)
	}
	if expired.Status != incidentmodel.ApprovalExpired {
		t.Fatalf("approval status = %s, want EXPIRED", expired.Status)
	}
	inc, _ := a.incidentSvc.Get(ctx, incidentID)
	if inc.Status != incidentmodel.StatusResolved {
		t.Fatalf("incident status = %s, want RESOLVED", inc.Status)
	}
	if left := decodePending(t, a.do(t, http.MethodGet, "/api/v1/approvals?status=PENDING", readToken, "", nil).Body.Bytes()); len(left) != 0 {
		t.Fatalf("pending approvals = %+v, want none after external recovery", left)
	}

	// 再批准必须失败，且 executor 调用为 0。
	if w := a.do(t, http.MethodPost, "/api/v1/approvals/"+ap.ID+"/approve", adminToken, "", nil); w.Code != http.StatusConflict {
		t.Fatalf("approve after recovery = %d, want 409", w.Code)
	}
	if got := a.restart.count(); got != 0 {
		t.Fatalf("restart endpoint calls = %d, want 0 after external recovery", got)
	}
}

func TestClosedLoop_Healthz(t *testing.T) {
	a := setup(t)
	w := a.do(t, http.MethodGet, "/healthz", "", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", w.Code)
	}
}
