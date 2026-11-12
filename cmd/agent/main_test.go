package main

import (
	"slices"
	"testing"

	"gooncall-agent/configs"
)

// TestBuildRegistryExplicitEnablement 验证业务工具与示例工具都需显式启用。
func TestBuildRegistryExplicitEnablement(t *testing.T) {
	cfg := &config.Config{}
	registry, err := buildRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Names()) != 0 {
		t.Fatalf("默认不应注册工具: %v", registry.Names())
	}

	cfg.Prometheus.Enabled = true
	cfg.Prometheus.URL = "http://prometheus.test:9090"
	cfg.RabbitMQ.Enabled = true
	cfg.RabbitMQ.ManagementURL = "http://rabbitmq.test:15672"
	cfg.RAG.Enabled = true
	cfg.Embedding.BaseURL = "http://embedding.test/v1"
	cfg.Embedding.Model = "test-embedding"
	cfg.Qdrant.URL = "http://qdrant.test:6334"
	cfg.Qdrant.Dim = 3

	registry, err = buildRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	names := registry.Names()
	for _, want := range []string{
		"prometheus_query", "prometheus_range_query", "prometheus_alerts",
		"rabbitmq_queue_status", "knowledge_search",
	} {
		if !slices.Contains(names, want) {
			t.Fatalf("缺少工具 %s: %v", want, names)
		}
	}
	if slices.Contains(names, "demo_read_file") || slices.Contains(names, "demo_write_file") {
		t.Fatal("示例工具默认不应注册")
	}
}
