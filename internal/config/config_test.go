package config

import (
	"os"
	"path/filepath"
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
  model: gpt-4o
agent:
  max_steps: 25
  timeout_seconds: 90
approval:
  enabled: false
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

	path := writeTempConfig(t, `postgres:
  dsn: ${POSTGRES_DSN}
redis:
  addr: ${REDIS_ADDR}
rabbitmq:
  url: ${RABBITMQ_URL}
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
}

func TestLoad_AppliesDefaults(t *testing.T) {
	path := writeTempConfig(t, `server: {}`)

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
