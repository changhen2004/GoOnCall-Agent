package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gooncall-agent/internal/execution/approval"
)

// TestHTTPExecutor_RequestShape 校验 JSON body、Bearer token、幂等键与 operation ID 解析。
func TestHTTPExecutor_RequestShape(t *testing.T) {
	var gotAuth, gotIdem, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotIdem = r.Header.Get("Idempotency-Key")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"operation_id":"op-123"}`))
	}))
	defer server.Close()

	executor, err := NewHTTPExecutor(HTTPExecutorConfig{URL: server.URL, Token: "secret-token", Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewHTTPExecutor() error = %v", err)
	}

	ctx := approval.WithApprovalID(context.Background(), "ap_1")
	out, err := executor.Execute(ctx, "restart_worker", `{"target":"worker","reason":"backlog"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotIdem != "ap_1" {
		t.Fatalf("Idempotency-Key = %q, want ap_1", gotIdem)
	}

	var body struct {
		Action     string          `json:"action"`
		ApprovalID string          `json:"approval_id"`
		Args       json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("decode body %q: %v", gotBody, err)
	}
	if body.Action != "restart_worker" || body.ApprovalID != "ap_1" {
		t.Fatalf("body = %+v", body)
	}
	if !strings.Contains(string(body.Args), `"target":"worker"`) {
		t.Fatalf("arguments not forwarded: %s", body.Args)
	}

	var result struct {
		Status      string `json:"status"`
		OperationID string `json:"operation_id"`
	}
	if err := json.Unmarshal([]byte(out.Output), &result); err != nil {
		t.Fatalf("decode result %q: %v", out.Output, err)
	}
	if result.OperationID != "op-123" || result.Status != "completed" || out.OperationID != "op-123" {
		t.Fatalf("result = %+v", result)
	}
}

// TestHTTPExecutor_NoApprovalID 没有幂等键时必须拒绝调用外部系统。
func TestHTTPExecutor_NoApprovalID(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	executor, _ := NewHTTPExecutor(HTTPExecutorConfig{URL: server.URL})
	if _, err := executor.Execute(context.Background(), "restart_worker", "{}"); err == nil {
		t.Fatal("Execute() without approval id should fail")
	}
	if called {
		t.Fatal("endpoint must not be called without an idempotency key")
	}
}

// TestHTTPExecutor_FailureClassification 4xx/5xx 是明确失败（可重试），不是结果未知。
func TestHTTPExecutor_FailureClassification(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("deployment failed"))
		}))
		executor, _ := NewHTTPExecutor(HTTPExecutorConfig{URL: server.URL, Timeout: time.Second})
		ctx := approval.WithApprovalID(context.Background(), "ap_1")

		_, err := executor.Execute(ctx, "restart_worker", "{}")
		if err == nil {
			t.Fatalf("status %d should fail", status)
		}
		if errors.Is(err, approval.ErrResultUnknown) {
			t.Fatalf("status %d must NOT be classified as unknown result: %v", status, err)
		}
		if !strings.Contains(err.Error(), "status") {
			t.Fatalf("error should mention status: %v", err)
		}
		server.Close()
	}
}

// TestHTTPExecutor_TimeoutIsUnknownResult 超时进入"结果未知"，禁止当作明确失败后自动重试。
func TestHTTPExecutor_TimeoutIsUnknownResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	executor, _ := NewHTTPExecutor(HTTPExecutorConfig{URL: server.URL, Timeout: 20 * time.Millisecond})
	ctx := approval.WithApprovalID(context.Background(), "ap_1")

	_, err := executor.Execute(ctx, "restart_worker", "{}")
	if err == nil {
		t.Fatal("timeout should fail")
	}
	if !errors.Is(err, approval.ErrResultUnknown) {
		t.Fatalf("timeout error = %v, want wrapped ErrResultUnknown", err)
	}
}

// TestHTTPExecutor_NoTokenLeak Token 与 Authorization header 不得出现在错误文本里。
func TestHTTPExecutor_NoTokenLeak(t *testing.T) {
	const token = "super-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 恶意/粗心的上游把 Authorization 原样回显。
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized: " + r.Header.Get("Authorization")))
	}))
	defer server.Close()

	executor, _ := NewHTTPExecutor(HTTPExecutorConfig{URL: server.URL, Token: token, Timeout: time.Second})
	ctx := approval.WithApprovalID(context.Background(), "ap_1")

	_, err := executor.Execute(ctx, "restart_worker", "{}")
	if err == nil {
		t.Fatal("401 should fail")
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "Bearer") {
		t.Fatalf("token leaked in error: %v", err)
	}
}

// TestHTTPExecutor_RequiresURL 缺少固定 URL 时构造失败。
func TestHTTPExecutor_RequiresURL(t *testing.T) {
	if _, err := NewHTTPExecutor(HTTPExecutorConfig{}); err == nil {
		t.Fatal("empty URL should be rejected")
	}
}

// TestTool_ExecuteUsesHTTPExecutor 工具在配置了 HTTP 适配器后走真实 HTTP，而不是 Mock。
func TestTool_ExecuteUsesHTTPExecutor(t *testing.T) {
	var gotIdem string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdem = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte(`{"operation_id":"op-9"}`))
	}))
	defer server.Close()

	tool, _ := newTestTool(true)
	tool.WithAllowedTargets([]string{"worker"})
	executor, err := NewHTTPExecutor(HTTPExecutorConfig{URL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewHTTPExecutor() error = %v", err)
	}
	tool.WithHTTPExecutor(executor)

	ctx := approval.WithApprovalID(context.Background(), "ap_http")
	out, err := tool.Execute(ctx, "restart_worker", `{"target":"worker"}`)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if gotIdem != "ap_http" {
		t.Fatalf("Idempotency-Key = %q, want ap_http", gotIdem)
	}
	if !strings.Contains(out.Output, "op-9") || out.OperationID != "op-9" {
		t.Fatalf("output = %+v, want operation id", out)
	}
}
