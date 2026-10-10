package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
)

// fakePublisher 记录发布调用，并可切换为失败。
type fakePublisher struct {
	calls   []string
	failFor int // >0 时前 N 次发布失败
}

func (p *fakePublisher) PublishAgentRequested(_ context.Context, incidentID string) error {
	p.calls = append(p.calls, incidentID)
	if p.failFor > 0 {
		p.failFor--
		return errors.New("rabbitmq publish not confirmed")
	}
	return nil
}

type alertTestApp struct {
	engine    *gin.Engine
	svc       *incidentservice.Service
	runRepo   *repository.MemoryRun
	publisher *fakePublisher
}

func newAlertTestApp(t *testing.T, failFirstPublish bool) *alertTestApp {
	t.Helper()
	gin.SetMode(gin.TestMode)

	incidentRepo := repository.NewMemory()
	svc := incidentservice.New(incidentRepo)
	runRepo := repository.NewMemoryRun()
	publisher := &fakePublisher{}
	if failFirstPublish {
		publisher.failFor = 1
	}

	h := NewAlertHandler(svc).WithPublisher(publisher).WithRunRepository(runRepo)
	r := gin.New()
	r.POST("/api/v1/alerts", h.Webhook)
	return &alertTestApp{engine: r, svc: svc, runRepo: runRepo, publisher: publisher}
}

func firingWebhook() dto.AlertWebhook {
	return dto.AlertWebhook{
		Status: "firing",
		Alerts: []dto.Alert{{
			Status:      "firing",
			Labels:      map[string]string{"alertname": "HighQueueDepth", "service": "gocommunity", "severity": "HIGH"},
			Annotations: map[string]string{"summary": "rabbitmq backlog"},
		}},
	}
}

func postWebhook(t *testing.T, app *alertTestApp, wh dto.AlertWebhook) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(wh)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.engine.ServeHTTP(w, req)
	return w
}

// TestWebhook_PublishFailureIsNot2xx 发布失败必须返回非 2xx 且把原因写进 errors。
func TestWebhook_PublishFailureIsNot2xx(t *testing.T) {
	app := newAlertTestApp(t, true)

	w := postWebhook(t, app, firingWebhook())
	if w.Code == http.StatusOK || w.Code < 400 {
		t.Fatalf("status = %d, want non-2xx when publishing fails, body=%s", w.Code, w.Body.String())
	}
	var resp dto.AlertWebhookResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("errors = %v, want one publish failure", resp.Errors)
	}
	// Incident 仍然创建成功（发布失败不该丢单）。
	if len(resp.Created) != 1 {
		t.Fatalf("created = %v, want 1", resp.Created)
	}
}

// TestWebhook_RetryRepublishesWhenOpenWithoutRun 发布失败后 Alertmanager 重试：命中去重仍要重新发布。
func TestWebhook_RetryRepublishesWhenOpenWithoutRun(t *testing.T) {
	app := newAlertTestApp(t, true)

	if w := postWebhook(t, app, firingWebhook()); w.Code == http.StatusOK {
		t.Fatalf("first publish should fail, status = %d", w.Code)
	}
	if len(app.publisher.calls) != 1 {
		t.Fatalf("publish calls = %d, want 1", len(app.publisher.calls))
	}

	w := postWebhook(t, app, firingWebhook())
	if w.Code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if len(app.publisher.calls) != 2 {
		t.Fatalf("publish calls = %d, want 2 (retry must republish)", len(app.publisher.calls))
	}
	if app.publisher.calls[0] != app.publisher.calls[1] {
		t.Fatalf("retry republished a different incident: %v", app.publisher.calls)
	}
}

// TestWebhook_NoRepublishWhenRunActive 已有有效 Run 时重复通知不再发布。
func TestWebhook_NoRepublishWhenRunActive(t *testing.T) {
	app := newAlertTestApp(t, false)
	ctx := context.Background()

	if w := postWebhook(t, app, firingWebhook()); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	incidents, err := app.svc.List(ctx, repository.ListFilter{})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("incidents = %v (%v), want 1", incidents, err)
	}
	if err := app.runRepo.CreateRun(ctx, &model.AgentRun{
		ID: "run_1", IncidentID: incidents[0].ID, AgentType: "diagnosis",
		Status: model.RunRunning, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}

	if w := postWebhook(t, app, firingWebhook()); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if len(app.publisher.calls) != 1 {
		t.Fatalf("publish calls = %d, want 1 (active run must suppress republish)", len(app.publisher.calls))
	}
}

// TestWebhook_NoRepublishWhenDiagnosisStarted Incident 已进入 INVESTIGATING 时不再发布。
func TestWebhook_NoRepublishWhenDiagnosisStarted(t *testing.T) {
	app := newAlertTestApp(t, false)
	ctx := context.Background()

	postWebhook(t, app, firingWebhook())
	incidents, _ := app.svc.List(ctx, repository.ListFilter{})
	if _, err := app.svc.Analyze(ctx, incidents[0].ID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if w := postWebhook(t, app, firingWebhook()); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if len(app.publisher.calls) != 1 {
		t.Fatalf("publish calls = %d, want 1", len(app.publisher.calls))
	}
}

// TestWebhook_ResolveDuringExecutionIsRetryable 处置进行中不能关单，并用非 2xx 让 Alertmanager 重试。
func TestWebhook_ResolveDuringExecutionIsRetryable(t *testing.T) {
	app := newAlertTestApp(t, false)
	ctx := context.Background()
	postWebhook(t, app, firingWebhook())
	incidents, _ := app.svc.List(ctx, repository.ListFilter{})
	for _, status := range []model.Status{model.StatusInvestigating, model.StatusWaitingApproval, model.StatusMitigating} {
		if _, err := app.svc.MoveTo(ctx, incidents[0].ID, status); err != nil {
			t.Fatalf("MoveTo(%s) error = %v", status, err)
		}
	}

	resolved := firingWebhook()
	resolved.Status = "resolved"
	resolved.Alerts[0].Status = "resolved"
	if w := postWebhook(t, app, resolved); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 so Alertmanager retries; body=%s", w.Code, w.Body.String())
	}
}
