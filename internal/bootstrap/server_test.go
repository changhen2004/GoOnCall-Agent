package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
)

type syncDiagnoser struct {
	calls int
}

func (d *syncDiagnoser) Diagnose(context.Context, *model.Incident, string) (*agentruntime.DiagnosisResult, error) {
	d.calls++
	return &agentruntime.DiagnosisResult{RunID: "run_sync", Conclusion: "同步诊断完成"}, nil
}

// readyApp 装配一个只用于 /readyz 的最小 App（handler 不参与就绪检查）。
func readyApp(t *testing.T, cfg *config.Config) *App {
	t.Helper()
	repo := repository.NewMemory()
	runRepo := repository.NewMemoryRun()
	broker := agentruntime.NewStreamBroker()
	return &App{
		cfg:          cfg,
		incidentSvc:  incidentservice.New(repo),
		incidentRepo: repo,
		runRepo:      runRepo,
		approvalRepo: repository.NewMemoryApproval(),
		approvalSvc:  approval.New(repository.NewMemoryApproval(), broker, nil),
		broker:       broker,
	}
}

func getReadyz(t *testing.T, a *App) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	a.buildServer().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readyz %q: %v", w.Body.String(), err)
	}
	return w.Code, body
}

// TestReadyz_DemoConfigIsReady 显式 Demo 配置（mock 处置、无 Token、内存仓库）应就绪。
func TestReadyz_DemoConfigIsReady(t *testing.T) {
	a := readyApp(t, config.Default())

	code, body := getReadyz(t, a)
	if code != http.StatusOK {
		t.Fatalf("readyz = %d %v, want 200", code, body)
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["config"] != "ok" {
		t.Fatalf("checks = %v, want config ok", checks)
	}
	// 未配置的依赖不注册检查。
	if _, ok := checks["postgres"]; ok {
		t.Fatalf("postgres check should not be registered without a DSN: %v", checks)
	}
	if _, ok := checks["rabbitmq"]; ok {
		t.Fatalf("rabbitmq check should not be registered without a URL: %v", checks)
	}
}

// TestReadyz_DangerousConfigIsNotReady 危险配置（http 处置缺 Token / URL / 白名单）必须非 200。
func TestReadyz_DangerousConfigIsNotReady(t *testing.T) {
	cfg := config.Default()
	cfg.Execution.Mode = config.ExecutionModeHTTP // 缺 restart_url / allowed_targets / Token

	a := readyApp(t, cfg)
	code, body := getReadyz(t, a)
	if code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("readyz = %d %v, want 503 not_ready", code, body)
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["config"] != "unavailable" {
		t.Fatalf("checks = %v, want config unavailable", checks)
	}
}

// TestReadyz_UnavailablePostgresIsNotReady PostgreSQL 不可用时必须非 200。
func TestReadyz_UnavailablePostgresIsNotReady(t *testing.T) {
	a := readyApp(t, config.Default())
	a.dbPing = func(context.Context) error { return errors.New("connection refused") }

	code, body := getReadyz(t, a)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("readyz = %d %v, want 503", code, body)
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["postgres"] != "unavailable" {
		t.Fatalf("checks = %v, want postgres unavailable", checks)
	}
}

// TestBuildServer_UsesSynchronousDiagnosisWithoutProducer 验证未启用消息发布时不会跳过同步诊断。
func TestBuildServer_UsesSynchronousDiagnosisWithoutProducer(t *testing.T) {
	a := readyApp(t, config.Default())
	diagnoser := &syncDiagnoser{}
	a.diagnoser = diagnoser
	a.prompt = "诊断提示词"
	inc, _, err := a.incidentSvc.Create(context.Background(), incidentservice.CreateIncidentInput{
		Service: "rabbitmq",
		Title:   "队列积压",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	a.buildServer().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/incidents/"+inc.ID+"/analyze", nil))
	if w.Code != http.StatusOK || diagnoser.calls != 1 {
		t.Fatalf("analyze = %d, calls = %d, body = %s", w.Code, diagnoser.calls, w.Body.String())
	}
	var body struct {
		Diagnosis *agentruntime.DiagnosisResult `json:"diagnosis"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode analyze response: %v", err)
	}
	if body.Diagnosis == nil || body.Diagnosis.Conclusion != "同步诊断完成" {
		t.Fatalf("diagnosis = %+v", body.Diagnosis)
	}
}
