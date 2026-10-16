package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoad_ReadsYAMLValues(t *testing.T) {
	path := writeTempConfig(t, `server:
  host: 127.0.0.1
  port: 9090
llm:
  provider: openai
  base_url: http://llm.example/v1
  api_key: llm-key
  model: gpt-4o
embedding:
  provider: openai
  base_url: http://embedding.example/v1
  api_key: embedding-key
  model: nomic-embed-text:latest
agent:
  max_steps: 25
  timeout_seconds: 90
approval:
  enabled: false
execution:
  mode: mock
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("Server.Host = %q, want %q", cfg.Server.Host, "127.0.0.1")
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want 9090", cfg.Server.Port)
	}
	if cfg.LLM.Provider != "openai" {
		t.Errorf("LLM.Provider = %q, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "gpt-4o" {
		t.Errorf("LLM.Model = %q, want gpt-4o", cfg.LLM.Model)
	}
	if cfg.LLM.BaseURL != "http://llm.example/v1" || cfg.LLM.APIKey != "llm-key" {
		t.Errorf("LLM = %+v, want independent chat endpoint", cfg.LLM)
	}
	if cfg.Embedding.BaseURL != "http://embedding.example/v1" ||
		cfg.Embedding.APIKey != "embedding-key" ||
		cfg.Embedding.Model != "nomic-embed-text:latest" {
		t.Errorf("Embedding = %+v, want independent embedding endpoint", cfg.Embedding)
	}
	if cfg.Agent.MaxSteps != 25 {
		t.Errorf("Agent.MaxSteps = %d, want 25", cfg.Agent.MaxSteps)
	}
	if cfg.Agent.TimeoutSeconds != 90 {
		t.Errorf("Agent.TimeoutSeconds = %d, want 90", cfg.Agent.TimeoutSeconds)
	}
	if cfg.Approval.Enabled {
		t.Errorf("Approval.Enabled = true, want false")
	}
}

func TestLoad_ExpandsEnvVars(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://u:p@localhost:5432/gooncall")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/")
	t.Setenv("EMBEDDING_BASE_URL", "http://localhost:11434/v1")

	path := writeTempConfig(t, `postgres:
  dsn: ${POSTGRES_DSN}
redis:
  addr: ${REDIS_ADDR}
rabbitmq:
  url: ${RABBITMQ_URL}
embedding:
  base_url: ${EMBEDDING_BASE_URL}
execution:
  mode: mock
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Postgres.DSN != "postgres://u:p@localhost:5432/gooncall" {
		t.Errorf("Postgres.DSN = %q", cfg.Postgres.DSN)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("Redis.Addr = %q, want localhost:6379", cfg.Redis.Addr)
	}
	if cfg.RabbitMQ.URL != "amqp://guest:guest@localhost:5672/" {
		t.Errorf("RabbitMQ.URL = %q", cfg.RabbitMQ.URL)
	}
	if cfg.Embedding.BaseURL != "http://localhost:11434/v1" {
		t.Errorf("Embedding.BaseURL = %q", cfg.Embedding.BaseURL)
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	path := writeTempConfig(t, "server: {}\nexecution:\n  mode: mock\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Agent.MaxSteps != 15 {
		t.Errorf("Agent.MaxSteps = %d, want 15", cfg.Agent.MaxSteps)
	}
	if cfg.Agent.MaxToolCalls != 20 {
		t.Errorf("Agent.MaxToolCalls = %d, want 20", cfg.Agent.MaxToolCalls)
	}
	if cfg.Agent.TimeoutSeconds != 180 {
		t.Errorf("Agent.TimeoutSeconds = %d, want 180", cfg.Agent.TimeoutSeconds)
	}
	if cfg.Qdrant.Collection != "gooncall_knowledge" {
		t.Errorf("Qdrant.Collection = %q", cfg.Qdrant.Collection)
	}
	if cfg.Qdrant.Dim != 768 {
		t.Errorf("Qdrant.Dim = %d, want 768", cfg.Qdrant.Dim)
	}
	if cfg.Embedding.Provider != "openai" || cfg.Embedding.Model != "nomic-embed-text:latest" {
		t.Errorf("Embedding defaults = %+v", cfg.Embedding)
	}
	if !cfg.Approval.Enabled {
		t.Errorf("Approval.Enabled = false, want true")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	if _, err := Load("/nonexistent/config.yaml"); err == nil {
		t.Fatal("Load() expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := writeTempConfig(t, `server: [unclosed`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
}

func TestLoad_ExecutionAndVerificationDefaults(t *testing.T) {
	path := writeTempConfig(t, "execution:\n  mode: mock\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Execution.Mode != ExecutionModeMock {
		t.Errorf("Execution.Mode = %q, want %q (显式 Demo)", cfg.Execution.Mode, ExecutionModeMock)
	}
	if cfg.Execution.TimeoutSeconds != 30 {
		t.Errorf("Execution.TimeoutSeconds = %d, want 30", cfg.Execution.TimeoutSeconds)
	}
	if cfg.Verification.Attempts != DefaultVerifyAttempts ||
		cfg.Verification.IntervalSeconds != DefaultVerifyIntervalSeconds ||
		cfg.Verification.RequiredSuccesses != DefaultVerifyRequiredSuccesses {
		t.Errorf("verification defaults = %+v", cfg.Verification)
	}
	if cfg.Auth.AdminActor == "" {
		t.Error("Auth.AdminActor should have a default actor")
	}
}

// TestLoad_EmptyExecutionModeIsNotAnExplicitDemo 未设置 EXECUTION_MODE 不能静默开启无认证 Demo。
func TestLoad_EmptyExecutionModeIsNotAnExplicitDemo(t *testing.T) {
	t.Setenv("EXECUTION_MODE", "")
	t.Setenv("VERIFICATION_MODE", "")

	path := writeTempConfig(t, `execution:
  mode: ${EXECUTION_MODE}
verification:
  mode: ${VERIFICATION_MODE}
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() = nil error, want an explicit execution.mode")
	}
}

// TestLoad_AllowedTargetsCSV 环境变量展开后是逗号分隔字符串，必须能解析成切片。
func TestLoad_AllowedTargetsCSV(t *testing.T) {
	t.Setenv("RESTART_WORKER_ALLOWED_TARGETS", " worker-a , worker-b ,, worker-c ")

	path := writeTempConfig(t, `execution:
  mode: mock
  allowed_targets: ${RESTART_WORKER_ALLOWED_TARGETS}
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"worker-a", "worker-b", "worker-c"}
	if len(cfg.Execution.AllowedTargets) != len(want) {
		t.Fatalf("AllowedTargets = %v, want %v", cfg.Execution.AllowedTargets, want)
	}
	for i := range want {
		if cfg.Execution.AllowedTargets[i] != want[i] {
			t.Fatalf("AllowedTargets = %v, want %v", cfg.Execution.AllowedTargets, want)
		}
	}
}

// TestValidate_DangerousCombinations 危险组合必须在启动时失败。
func TestValidate_DangerousCombinations(t *testing.T) {
	httpBase := func() *Config {
		cfg := Default()
		cfg.Execution.Mode = ExecutionModeHTTP
		cfg.Execution.RestartURL = "http://restart.internal/restart"
		cfg.Execution.AllowedTargets = CSVList{"worker-a"}
		cfg.Verification.Mode = "prometheus"
		cfg.Prometheus.URL = "http://prometheus:9090"
		cfg.Auth = AuthConfig{WebhookToken: "w", ReadToken: "r", AdminToken: "a"}
		cfg.Postgres.DSN = "postgres://db/gooncall"
		cfg.RabbitMQ.URL = "amqp://rabbitmq/"
		cfg.LLM.BaseURL = "http://llm/v1"
		cfg.LLM.Model = "model"
		return cfg
	}

	if err := httpBase().Validate(); err != nil {
		t.Fatalf("valid http config rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"unknown execution mode", func(c *Config) { c.Execution.Mode = "shell" }},
		{"http without restart url", func(c *Config) { c.Execution.RestartURL = "" }},
		{"http without allowed targets", func(c *Config) { c.Execution.AllowedTargets = nil }},
		{"http with mock verification", func(c *Config) { c.Verification.Mode = "mock" }},
		{"http without webhook token", func(c *Config) { c.Auth.WebhookToken = "" }},
		{"http without read token", func(c *Config) { c.Auth.ReadToken = "" }},
		{"http without admin token", func(c *Config) { c.Auth.AdminToken = "" }},
		{"http without postgres", func(c *Config) { c.Postgres.DSN = "" }},
		{"http without rabbitmq", func(c *Config) { c.RabbitMQ.URL = "" }},
		{"http without llm base url", func(c *Config) { c.LLM.BaseURL = "" }},
		{"http without llm model", func(c *Config) { c.LLM.Model = "" }},
		{"prometheus without url", func(c *Config) { c.Prometheus.URL = "" }},
		{"required exceeds attempts", func(c *Config) { c.Verification.Attempts = 1; c.Verification.RequiredSuccesses = 3 }},
		{"zero attempts", func(c *Config) { c.Verification.Attempts = 0 }},
		{"unknown verification mode", func(c *Config) { c.Verification.Mode = "guess" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := httpBase()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() = nil, want an error for %s", tc.name)
			}
		})
	}
}

// TestValidate_MockModeAllowsEmptyTokens mock（显式 Demo）允许空 Token，也不要求 HTTP 参数。
func TestValidate_MockModeAllowsEmptyTokens(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidate_RerankerRequiresBaseURL(t *testing.T) {
	cfg := Default()
	cfg.RAG.Reranker.Enabled = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "rag.reranker.base_url") {
		t.Fatalf("Validate() error = %v, want reranker base URL error", err)
	}

	cfg.RAG.Reranker.BaseURL = "http://reranker:80"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid reranker config rejected: %v", err)
	}
}

func TestValidate_QwenRerankerRequiresCredentialsAndModel(t *testing.T) {
	cfg := Default()
	cfg.RAG.Reranker = RerankerConfig{
		Enabled:  true,
		Provider: RerankerProviderQwen,
		BaseURL:  "https://example.invalid/compatible-api/v1",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "rag.reranker.api_key") {
		t.Fatalf("Validate() error = %v, want reranker API key error", err)
	}

	cfg.RAG.Reranker.APIKey = "test-key"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "rag.reranker.model") {
		t.Fatalf("Validate() error = %v, want reranker model error", err)
	}

	cfg.RAG.Reranker.Model = "qwen3-rerank"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid Qwen reranker config rejected: %v", err)
	}
}

func TestValidate_RejectsUnknownRerankerProvider(t *testing.T) {
	cfg := Default()
	cfg.RAG.Reranker = RerankerConfig{
		Enabled:  true,
		Provider: "unknown",
		BaseURL:  "http://reranker:80",
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "rag.reranker.provider") {
		t.Fatalf("Validate() error = %v, want reranker provider error", err)
	}
}

// TestValidate_DoesNotLeakSecrets 校验错误不得回显 Token / 凭证。
func TestValidate_DoesNotLeakSecrets(t *testing.T) {
	const adminSecret = "super-secret-admin-token"
	const restartSecret = "super-secret-restart-token"

	cfg := Default()
	cfg.Execution.Mode = ExecutionModeHTTP
	cfg.Execution.RestartURL = "http://restart.internal/restart"
	cfg.Execution.Token = restartSecret
	cfg.Execution.AllowedTargets = CSVList{"worker-a"}
	cfg.Verification.Mode = "prometheus"
	cfg.Prometheus.URL = "http://prometheus:9090"
	// 故意只配 webhook token：admin token 缺失。
	cfg.Auth = AuthConfig{WebhookToken: "webhook-token-abc", ReadToken: "read-token-def", AdminToken: ""}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want an error")
	}
	msg := err.Error()
	if strings.Contains(msg, adminSecret) || strings.Contains(msg, restartSecret) {
		t.Fatalf("error leaks a secret: %s", msg)
	}
	if strings.Contains(msg, cfg.Auth.WebhookToken) || strings.Contains(msg, cfg.Auth.ReadToken) {
		t.Fatalf("error echoes a configured token: %s", msg)
	}
}

// TestLoad_RejectsDangerousConfig Load 必须拒绝危险组合（而不是运行到一半才失败）。
func TestLoad_RejectsDangerousConfig(t *testing.T) {
	path := writeTempConfig(t, `execution:
  mode: http
  restart_url: http://restart.internal/restart
  allowed_targets: worker-a
verification:
  mode: mock
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() = nil error, want rejection of execution.mode=http with mock verification")
	}
}
