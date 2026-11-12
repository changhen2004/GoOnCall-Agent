package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	agentapi "gooncall-agent/internal/api/agent"
	"gooncall-agent/internal/auth"
	kmodel "gooncall-agent/internal/knowledge/model"
	"gooncall-agent/internal/knowledge/retriever"
	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/session"
	"gooncall-agent/internal/store"
	"gooncall-agent/internal/tools"
	"gooncall-agent/internal/tools/ops"
)

type staticProvider struct{}

func (staticProvider) Name() string { return "static" }
func (staticProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: "完成", FinishReason: "stop"}, nil
}
func (staticProvider) ChatStream(context.Context, llm.Request) (llm.Stream, error) {
	return &staticStream{response: &llm.Response{Content: "完成", FinishReason: "stop"}}, nil
}

type staticStream struct {
	response *llm.Response
	sent     bool
}

func (s *staticStream) Recv() (llm.StreamChunk, error) {
	if s.sent {
		return llm.StreamChunk{}, io.EOF
	}
	s.sent = true
	return llm.StreamChunk{Content: s.response.Content, FinishReason: s.response.FinishReason}, nil
}
func (s *staticStream) Response() (*llm.Response, error) { return s.response, nil }
func (s *staticStream) Close() error                     { return nil }

type testEnv struct {
	server *httptest.Server
	db     *store.DB
	auth   *auth.Service
	svc    *session.Service
}

type blockingProvider struct{}

func (blockingProvider) Name() string { return "blocking" }
func (blockingProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("unused")
}
func (blockingProvider) ChatStream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	return newEnvWithProvider(t, staticProvider{})
}

func newEnvWithProvider(t *testing.T, provider llm.Provider) *testEnv {
	t.Helper()
	return newEnvWithTools(t, provider, tools.NewRegistry())
}

func newEnvWithTools(t *testing.T, provider llm.Provider, registry *tools.Registry) *testEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	authSvc, err := auth.New(db, auth.Options{
		SessionTTL: time.Hour, LoginMaxAttempts: 3,
		LoginWindow: time.Minute, LoginLimiterCapacity: 64,
	})
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	svc, err := session.New(db, session.Options{
		Models: map[string]session.Model{
			"default": {Provider: provider, ContextWindow: 8192, MaxOutputTokens: 512},
		},
		DefaultModel: "default",
		MaxRounds:    4,
		RunBudget:    time.Minute,
		ApprovalTTL:  time.Minute,
		Registry:     registry,
		ProcessID:    "test",
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	})

	server := httptest.NewServer(agentapi.New(svc, authSvc, nil))
	t.Cleanup(server.Close)
	return &testEnv{server: server, db: db, auth: authSvc, svc: svc}
}

func (e *testEnv) createUser(t *testing.T, username, password string) *auth.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	user := &auth.User{
		ID: uuid.NewString(), Username: username, PasswordHash: hash,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.db.CreateUser(context.Background(), *user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

func (e *testEnv) login(t *testing.T, username, password string) (int, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	return e.request(t, http.MethodPost, "/v1/auth/login", "", body)
}

func (e *testEnv) request(t *testing.T, method, path, token string, body []byte) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func (e *testEnv) loginToken(t *testing.T, username, password string) string {
	t.Helper()
	status, body := e.login(t, username, password)
	if status != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", status, body)
	}
	var result auth.LoginResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result.Token
}

func TestLoginLogoutAndCrossUserIsolation(t *testing.T) {
	env := newEnv(t)
	alice := env.createUser(t, "alice", "password-123")
	env.createUser(t, "bob", "password-456")

	// 未知账号与错误密码返回相同响应。
	badStatus, badBody := env.login(t, "alice", "wrong-password")
	missingStatus, missingBody := env.login(t, "missing", "wrong-password")
	if badStatus != http.StatusUnauthorized || missingStatus != http.StatusUnauthorized ||
		!bytes.Equal(badBody, missingBody) {
		t.Fatalf("登录失败响应不一致: %d %s / %d %s", badStatus, badBody, missingStatus, missingBody)
	}

	aliceToken := env.loginToken(t, "alice", "password-123")
	bobToken := env.loginToken(t, "bob", "password-456")

	status, body := env.request(t, http.MethodGet, "/v1/me", aliceToken, nil)
	if status != http.StatusOK || !bytes.Contains(body, []byte(alice.ID)) {
		t.Fatalf("me = %d %s", status, body)
	}

	status, body = env.request(t, http.MethodPost, "/v1/conversations", aliceToken, []byte(`{}`))
	if status != http.StatusCreated {
		t.Fatalf("create conversation = %d %s", status, body)
	}
	var conversation session.Conversation
	if err := json.Unmarshal(body, &conversation); err != nil {
		t.Fatal(err)
	}

	status, body = env.request(t, http.MethodPost,
		"/v1/conversations/"+conversation.ID+"/messages", aliceToken,
		[]byte(`{"content":"你好"}`))
	if status != http.StatusAccepted {
		t.Fatalf("submit = %d %s", status, body)
	}
	var submitted struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(body, &submitted); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		status, body := env.request(t, http.MethodGet, "/v1/runs/"+submitted.RunID, aliceToken, nil)
		return status == http.StatusOK && bytes.Contains(body, []byte(`"status":"completed"`))
	})

	// 其他用户不能访问会话、Run、SSE、取消与审批。
	crossPaths := []struct {
		method string
		path   string
		body   []byte
	}{
		{http.MethodGet, "/v1/conversations/" + conversation.ID, nil},
		{http.MethodGet, "/v1/runs/" + submitted.RunID, nil},
		{http.MethodGet, "/v1/runs/" + submitted.RunID + "/events", nil},
		{http.MethodPost, "/v1/runs/" + submitted.RunID + "/cancel", nil},
		{http.MethodGet, "/v1/runs/" + submitted.RunID + "/approvals", nil},
	}
	for _, item := range crossPaths {
		status, _ := env.request(t, item.method, item.path, bobToken, item.body)
		if status != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403", item.method, item.path, status)
		}
	}

	// 旧固定 Token / 任意随机 Token 不再有效。
	if status, _ := env.request(t, http.MethodGet, "/v1/me", "old-static-token", nil); status != http.StatusUnauthorized {
		t.Fatalf("旧 Token 状态 = %d", status)
	}

	// 普通退出只撤销当前 Token，另一台设备保持有效。
	if status, _ := env.request(t, http.MethodPost, "/v1/auth/logout", aliceToken, nil); status != http.StatusNoContent {
		t.Fatalf("logout = %d", status)
	}
	if status, _ := env.request(t, http.MethodPost, "/v1/auth/logout", aliceToken, nil); status != http.StatusNoContent {
		t.Fatalf("重复 logout = %d", status)
	}
	if status, _ := env.request(t, http.MethodGet, "/v1/me", aliceToken, nil); status != http.StatusUnauthorized {
		t.Fatalf("退出后 me = %d", status)
	}
	if status, _ := env.request(t, http.MethodGet, "/v1/me", bobToken, nil); status != http.StatusOK {
		t.Fatalf("其他用户 Token 不应受影响: %d", status)
	}
}

func TestDisableAndPasswordResetRevokeAccess(t *testing.T) {
	env := newEnv(t)
	user := env.createUser(t, "alice", "password-123")
	token := env.loginToken(t, "alice", "password-123")

	if changed, err := env.db.DisableUser(context.Background(), user.ID, time.Now()); err != nil || !changed {
		t.Fatalf("DisableUser = %v, %v", changed, err)
	}
	if status, _ := env.request(t, http.MethodGet, "/v1/me", token, nil); status != http.StatusUnauthorized {
		t.Fatalf("禁用后 me = %d", status)
	}
	if status, _ := env.login(t, "alice", "password-123"); status != http.StatusUnauthorized {
		t.Fatalf("禁用后登录 = %d", status)
	}

	if changed, err := env.db.EnableUser(context.Background(), user.ID, time.Now()); err != nil || !changed {
		t.Fatalf("EnableUser = %v, %v", changed, err)
	}
	if status, _ := env.request(t, http.MethodGet, "/v1/me", token, nil); status != http.StatusUnauthorized {
		t.Fatalf("重新启用不应恢复旧 Token: %d", status)
	}

	newToken := env.loginToken(t, "alice", "password-123")
	hash, _ := auth.HashPassword("new-password-123")
	if changed, err := env.db.ResetUserPassword(context.Background(), user.ID, hash, time.Now()); err != nil || !changed {
		t.Fatalf("ResetUserPassword = %v, %v", changed, err)
	}
	if status, _ := env.request(t, http.MethodGet, "/v1/me", newToken, nil); status != http.StatusUnauthorized {
		t.Fatalf("重置密码后旧 Token = %d", status)
	}
	if status, _ := env.login(t, "alice", "new-password-123"); status != http.StatusOK {
		t.Fatalf("新密码登录 = %d", status)
	}
}

func TestLoginRateLimitAndLegacyOwnership(t *testing.T) {
	env := newEnv(t)
	env.createUser(t, "ratelimit-user", "password-123")

	for i := 0; i < 3; i++ {
		_, _ = env.login(t, "ratelimit-user", "wrong-password")
	}
	if status, _ := env.login(t, "ratelimit-user", "wrong-password"); status != http.StatusTooManyRequests {
		t.Fatalf("登录限制状态 = %d", status)
	}

	// 同名新账号不能继承旧固定 Token 身份的数据。
	env.createUser(t, "alice", "password-123")
	legacy := session.Conversation{ID: "legacy-2", Owner: "alice", OwnerKind: session.OwnerKindLegacy}
	if err := env.db.CreateConversation(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	token := env.loginToken(t, "alice", "password-123")
	if status, _ := env.request(t, http.MethodGet, "/v1/conversations/legacy-2", token, nil); status != http.StatusForbidden {
		t.Fatalf("旧身份会话不应被同名账号继承: %d", status)
	}
}

type scriptedProvider struct {
	responses []*llm.Response
	index     int
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("unused")
}
func (p *scriptedProvider) ChatStream(context.Context, llm.Request) (llm.Stream, error) {
	response := p.responses[p.index]
	if p.index < len(p.responses)-1 {
		p.index++
	}
	return &staticStream{response: response}, nil
}

type knowledgeStub struct{}

func (knowledgeStub) Retrieve(context.Context, string, int) ([]retriever.Result, error) {
	return []retriever.Result{{
		Chunk: &kmodel.Chunk{
			Source: "rabbitmq.md", DocID: "runbook.rabbitmq.queue_backlog",
			Title: "队列积压", Content: "检查消费者数量与消息积压", LastVerifiedAt: "2026-01-01",
		},
		Score: 0.9,
	}}, nil
}

// TestAgentChainUsesMonitoringAndKnowledgeTools 验证“提问 → 监控 → 知识 → 回答”的新链路。
func TestAgentChainUsesMonitoringAndKnowledgeTools(t *testing.T) {
	prometheusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"success","data":{"alerts":[
			{"labels":{"alertname":"HighErrorRate","severity":"critical","service":"orders"},
			 "annotations":{"summary":"5xx 错误率升高"},"state":"firing","value":"1"}
		]}}`))
	}))
	defer prometheusServer.Close()

	registry := tools.NewRegistry()
	for _, tool := range ops.NewPrometheusTools(ops.PrometheusOptions{URL: prometheusServer.URL}) {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(ops.NewKnowledgeTool(ops.KnowledgeOptions{
		Provider: func(context.Context) (retriever.Retriever, error) { return knowledgeStub{}, nil },
	})); err != nil {
		t.Fatal(err)
	}

	provider := &scriptedProvider{responses: []*llm.Response{
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID: "p1", Type: "function",
				Function: llm.FunctionCall{Name: "prometheus_alerts", Arguments: `{}`},
			}},
		},
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID: "k1", Type: "function",
				Function: llm.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"队列积压"}`},
			}},
		},
		{Content: "告警与 Runbook 已确认", FinishReason: "stop"},
	}}

	env := newEnvWithTools(t, provider, registry)
	env.createUser(t, "alice", "password-123")
	token := env.loginToken(t, "alice", "password-123")

	status, body := env.request(t, http.MethodPost, "/v1/conversations", token, []byte(`{}`))
	if status != http.StatusCreated {
		t.Fatalf("create conversation = %d %s", status, body)
	}
	var conversation session.Conversation
	if err := json.Unmarshal(body, &conversation); err != nil {
		t.Fatal(err)
	}
	status, body = env.request(t, http.MethodPost,
		"/v1/conversations/"+conversation.ID+"/messages", token,
		[]byte(`{"content":"查看告警并检索 Runbook"}`))
	if status != http.StatusAccepted {
		t.Fatalf("submit = %d %s", status, body)
	}
	var submitted struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(body, &submitted); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool {
		status, body := env.request(t, http.MethodGet, "/v1/runs/"+submitted.RunID, token, nil)
		return status == http.StatusOK && bytes.Contains(body, []byte(`"status":"completed"`))
	})
	status, body = env.request(t, http.MethodGet, "/v1/conversations/"+conversation.ID, token, nil)
	if status != http.StatusOK {
		t.Fatalf("conversation = %d %s", status, body)
	}
	for _, want := range []string{"HighErrorRate", "rabbitmq.md", "告警与 Runbook 已确认"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("会话缺少 %q: %s", want, body)
		}
	}
}

func TestLogoutDoesNotCancelBackgroundRun(t *testing.T) {
	env := newEnvWithProvider(t, blockingProvider{})
	env.createUser(t, "alice", "password-123")
	token := env.loginToken(t, "alice", "password-123")

	status, body := env.request(t, http.MethodPost, "/v1/conversations", token, []byte(`{}`))
	if status != http.StatusCreated {
		t.Fatalf("create conversation = %d %s", status, body)
	}
	var conversation session.Conversation
	if err := json.Unmarshal(body, &conversation); err != nil {
		t.Fatal(err)
	}
	status, body = env.request(t, http.MethodPost,
		"/v1/conversations/"+conversation.ID+"/messages", token, []byte(`{"content":"开始"}`))
	if status != http.StatusAccepted {
		t.Fatalf("submit = %d %s", status, body)
	}
	var submitted struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(body, &submitted); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		status, body := env.request(t, http.MethodGet, "/v1/runs/"+submitted.RunID, token, nil)
		return status == http.StatusOK && bytes.Contains(body, []byte(`"status":"running"`))
	})

	if status, _ := env.request(t, http.MethodPost, "/v1/auth/logout", token, nil); status != http.StatusNoContent {
		t.Fatalf("logout = %d", status)
	}

	newToken := env.loginToken(t, "alice", "password-123")
	status, body = env.request(t, http.MethodGet, "/v1/runs/"+submitted.RunID, newToken, nil)
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"status":"running"`)) {
		t.Fatalf("普通退出不应取消后台运行: %d %s", status, body)
	}
	// 清理运行，避免测试结束时后台 goroutine 仍在等待。
	_, _ = env.request(t, http.MethodPost, "/v1/runs/"+submitted.RunID+"/cancel", newToken, nil)
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
