package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const baseConfig = `
llm:
  base_url: https://example.test/v1
  model: test-model
`

func TestValidateBusinessEnablement(t *testing.T) {
	cfg, err := Load(writeConfig(t, baseConfig+`
prometheus:
  enabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "prometheus.url") {
		t.Fatalf("prometheus 配置错误 = %v", err)
	}

	cfg, err = Load(writeConfig(t, baseConfig+`
rag:
  enabled: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidateRAG(); err == nil || !strings.Contains(err.Error(), "embedding") {
		t.Fatalf("RAG 配置错误 = %v", err)
	}
}

func TestDefaultsAndEnvironmentOverrides(t *testing.T) {
	t.Setenv("AGENT_SESSION_TTL", "2h")
	t.Setenv("PROMETHEUS_URL", "http://prometheus.test:9090")
	cfg, err := Load(writeConfig(t, baseConfig+`
agent_service:
  listen_addr: 127.0.0.1:9999
`+fmt.Sprintf(`
prometheus:
  enabled: true
  url: %s
`, "${PROMETHEUS_URL}")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionTTL().Hours() != 2 {
		t.Fatalf("session ttl = %v", cfg.SessionTTL())
	}
	if cfg.Prometheus.URL != "http://prometheus.test:9090" {
		t.Fatalf("prometheus url = %s", cfg.Prometheus.URL)
	}
	if cfg.Auth.LoginMaxAttempts != 5 || cfg.Tool.TimeoutSeconds != 30 {
		t.Fatalf("默认值未生效: %+v %+v", cfg.Auth, cfg.Tool)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
