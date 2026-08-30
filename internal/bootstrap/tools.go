package bootstrap

import (
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/knowledge/retriever"
	incidenttool "gooncall-agent/internal/tool/incident"
	"gooncall-agent/internal/tool/prometheus"
	"gooncall-agent/internal/tool/rabbitmq"
	"gooncall-agent/internal/tool/registry"
	"gooncall-agent/internal/tool/runbook"
)

// buildToolRegistry 注册核心只读工具（按配置启用）。
func buildToolRegistry(cfg *config.Config, repo repository.Repository, rag retriever.Retriever) *registry.Registry {
	reg := registry.New()

	if t, err := incidenttool.New(repo).EinoTool(); err == nil {
		_ = reg.Register(t, registry.RiskLow)
	}

	var runbookTool *runbook.Tool
	if rag != nil {
		runbookTool = runbook.NewWithRetriever(rag, cfg.RAG.TopK)
	} else {
		runbookTool = runbook.New("docs")
	}
	if t, err := runbookTool.EinoTool(); err == nil {
		_ = reg.Register(t, registry.RiskLow)
	}

	if cfg.Prometheus.URL != "" {
		pt := prometheus.New(cfg.Prometheus.URL)
		// 即时查询（prometheus.query）+ 活跃告警（prometheus.alerts）+ 范围查询（prometheus.range_query）。
		if t, err := pt.EinoTool(); err == nil {
			_ = reg.Register(t, registry.RiskLow)
		}
		if t, err := pt.AlertsEinoTool(); err == nil {
			_ = reg.Register(t, registry.RiskLow)
		}
		if t, err := pt.RangeEinoTool(); err == nil {
			_ = reg.Register(t, registry.RiskLow)
		}
	}

	if cfg.RabbitMQ.ManagementURL != "" {
		if t, err := rabbitmq.New(cfg.RabbitMQ.ManagementURL, cfg.RabbitMQ.Username, cfg.RabbitMQ.Password).EinoTool(); err == nil {
			_ = reg.Register(t, registry.RiskLow)
		}
	}

	return reg
}
