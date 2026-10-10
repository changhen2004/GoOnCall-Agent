package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func readyBody(t *testing.T, h *HealthHandler) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/readyz", h.Ready)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readyz body %q: %v", w.Body.String(), err)
	}
	return w.Code, body
}

// TestReady_AllChecksPass 依赖可用时 200，并逐个报告检查结果。
func TestReady_AllChecksPass(t *testing.T) {
	h := NewHealthHandler().
		WithCheck("config", func(context.Context) error { return nil }).
		WithCheck("postgres", func(context.Context) error { return nil })

	code, body := readyBody(t, h)
	if code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v, want 200 ready", code, body)
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["config"] != "ok" || checks["postgres"] != "ok" {
		t.Fatalf("checks = %v", checks)
	}
}

// TestReady_FailingDependencyIsNotReady 任一依赖失败都必须返回非 200。
func TestReady_FailingDependencyIsNotReady(t *testing.T) {
	secret := "postgres://user:super-secret@127.0.0.1:5432/db"
	h := NewHealthHandler().
		WithCheck("config", func(context.Context) error { return nil }).
		WithCheck("postgres", func(context.Context) error {
			return errors.New("dial " + secret + ": connection refused")
		})

	code, body := readyBody(t, h)
	if code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("readyz = %d %v, want 503 not_ready", code, body)
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["postgres"] != "unavailable" {
		t.Fatalf("checks = %v, want postgres unavailable", checks)
	}
	// 依赖错误原文可能带 DSN / 凭证，不能出现在响应里。
	if raw, _ := json.Marshal(body); string(raw) != "" && strings.Contains(string(raw), "super-secret") {
		t.Fatalf("readyz leaks a credential: %s", raw)
	}
}

// TestReady_DangerousConfigIsNotReady 危险配置（例如 http 处置却缺 Token）必须非 200。
func TestReady_DangerousConfigIsNotReady(t *testing.T) {
	h := NewHealthHandler().WithCheck("config", func(context.Context) error {
		return errors.New("auth.admin_token is required when execution.mode=http")
	})

	code, body := readyBody(t, h)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("readyz = %d %v, want 503", code, body)
	}
}

// TestReady_NoChecks 没有配置任何检查时仍然就绪（显式 Demo / 内存仓库）。
func TestReady_NoChecks(t *testing.T) {
	code, body := readyBody(t, NewHealthHandler())
	if code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v, want 200 ready", code, body)
	}
}

// TestHealth_AlwaysOk 存活检查不依赖外部组件。
func TestHealth_AlwaysOk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/healthz", NewHealthHandler().Health)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", w.Code)
	}
}
