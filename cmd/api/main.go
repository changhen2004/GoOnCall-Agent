// GoOnCall Agent API 入口。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/api/router"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/execution/executor"
	"gooncall-agent/internal/execution/policy"
	"gooncall-agent/internal/execution/postmortem"
	"gooncall-agent/internal/execution/verifier"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/knowledge/embedding"
	"gooncall-agent/internal/knowledge/loader"
	"gooncall-agent/internal/knowledge/retriever"
	"gooncall-agent/internal/knowledge/splitter"
	"gooncall-agent/internal/knowledge/vectorstore"
	"gooncall-agent/internal/storage/postgres"
	"gooncall-agent/internal/tool/deployment"
	incidenttool "gooncall-agent/internal/tool/incident"
	"gooncall-agent/internal/tool/prometheus"
	"gooncall-agent/internal/tool/rabbitmq"
	"gooncall-agent/internal/tool/registry"
	"gooncall-agent/internal/tool/runbook"
)

const diagnosisPromptPath = "prompts/diagnosis/system.md"

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "configs/config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("load config", "error", err, "path", cfgPath)
		os.Exit(1)
	}

	incidentRepo, err := buildIncidentRepo(cfg)
	if err != nil {
		slog.Error("init incident repository", "error", err)
		os.Exit(1)
	}

	svc := incidentservice.New(incidentRepo)

	runRepo, err := buildRunRepo(cfg)
	if err != nil {
		slog.Error("init run repository", "error", err)
		os.Exit(1)
	}
	broker := agentruntime.NewStreamBroker()

	approvalRepo, err := buildApprovalRepo(cfg)
	if err != nil {
		slog.Error("init approval repository", "error", err)
		os.Exit(1)
	}
	policyEngine := policy.New(cfg.Approval.Enabled)
	restartTool := deployment.New(nil, policyEngine)
	// 处置编排：Execute -> Verify -> Resolve -> Postmortem
	remediation := executor.NewRemediation(
		restartTool,
		verifier.New(verifier.DefaultConfig()),
		&executor.SimulatedGatherer{Metrics: verifier.Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001}},
		svc,
		runRepo,
		postmortem.New(),
		broker,
	)
	approvalSvc := approval.New(approvalRepo, broker, remediation)
	restartTool.SetApproval(approvalSvc)

	ragRetriever, err := buildRetriever(cfg)
	if err != nil {
		slog.Error("build RAG retriever", "error", err)
		os.Exit(1)
	}
	reg := buildToolRegistry(cfg, incidentRepo, ragRetriever)

	// 自动处置工具（MEDIUM，需人工审批）
	if t, err := restartTool.EinoTool(); err == nil {
		_ = reg.Register(t, registry.RiskMedium)
	}

	// 诊断 Agent（可选：未配置 LLM 时 analyze 仅做状态流转）
	var diagnoser handler.Diagnoser
	prompt := ""
	if cfg.LLM.BaseURL != "" && cfg.LLM.Model != "" {
		chatModel := agentruntime.NewOpenAIChatModel(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.Model)
		rt := agentruntime.New(chatModel, reg, cfg.Agent.MaxSteps).WithRunRecording(runRepo, broker)
		diagnoser = rt
		prompt = loadPrompt(diagnosisPromptPath)
		slog.Info("agent runtime enabled", "model", cfg.LLM.Model, "tools", reg.Names())
	} else {
		slog.Warn("LLM not configured (LLM_BASE_URL / LLM_MODEL empty), analyze will skip diagnosis")
	}

	incidentHandler := handler.NewIncidentHandler(svc).WithDiagnoser(diagnoser, prompt)
	runHandler := handler.NewRunHandler(runRepo, broker)
	approvalHandler := handler.NewApprovalHandler(approvalSvc)
	alertHandler := handler.NewAlertHandler(svc)
	engine := router.New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler)
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	slog.Info("starting gooncall api", "addr", addr)
	if err := engine.Run(addr); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}

// buildIncidentRepo 根据配置选择 PostgreSQL 或内存仓库。
func buildIncidentRepo(cfg *config.Config) (repository.Repository, error) {
	if cfg.Postgres.DSN == "" {
		slog.Warn("POSTGRES_DSN is empty, falling back to in-memory repository")
		return repository.NewMemory(), nil
	}

	db, err := postgres.Connect(cfg.Postgres.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	pgRepo := repository.NewPostgres(db)
	if err := pgRepo.AutoMigrate(); err != nil {
		return nil, fmt.Errorf("auto migrate: %w", err)
	}
	slog.Info("postgres connected and migrated")
	return pgRepo, nil
}

// buildApprovalRepo 根据配置选择 PostgreSQL 或内存审批仓库。
func buildApprovalRepo(cfg *config.Config) (repository.ApprovalRepository, error) {
	if cfg.Postgres.DSN == "" {
		return repository.NewMemoryApproval(), nil
	}

	db, err := postgres.Connect(cfg.Postgres.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	pgApproval := repository.NewPostgresApproval(db)
	if err := pgApproval.AutoMigrate(); err != nil {
		return nil, fmt.Errorf("auto migrate approval: %w", err)
	}
	return pgApproval, nil
}

// buildRunRepo 根据配置选择 PostgreSQL 或内存 Run 仓库。
func buildRunRepo(cfg *config.Config) (repository.RunRepository, error) {
	if cfg.Postgres.DSN == "" {
		return repository.NewMemoryRun(), nil
	}

	db, err := postgres.Connect(cfg.Postgres.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	pgRun := repository.NewPostgresRun(db)
	if err := pgRun.AutoMigrate(); err != nil {
		return nil, fmt.Errorf("auto migrate run: %w", err)
	}
	return pgRun, nil
}

// buildRetriever 构建 RAG 混合检索器（未配置 LLM/Embedding 时返回 nil）。
func buildRetriever(cfg *config.Config) (retriever.Retriever, error) {
	if cfg.LLM.BaseURL == "" || cfg.LLM.EmbeddingModel == "" {
		slog.Warn("RAG disabled (LLM_BASE_URL / LLM_EMBEDDING_MODEL empty), runbook.search uses lexical search")
		return nil, nil
	}

	embedder := embedding.NewOpenAIEmbedder(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.EmbeddingModel)

	// 向量存储：内存实现（生产可切换 vectorstore.NewQdrant 接入 Qdrant）。
	store := vectorstore.NewMemory()

	ld := loader.New("docs", splitter.New(1000))
	chunks, err := ld.Load(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load knowledge docs: %w", err)
	}
	if len(chunks) == 0 {
		slog.Warn("no knowledge docs found under docs/")
		return nil, nil
	}

	h := retriever.NewHybrid(chunks, embedder, store)
	if err := h.Index(context.Background()); err != nil {
		return nil, fmt.Errorf("index knowledge: %w", err)
	}
	slog.Info("knowledge indexed", "chunks", len(chunks))
	return h, nil
}

// buildToolRegistry 注册核心工具（按配置启用）。
func buildToolRegistry(cfg *config.Config, repo repository.Repository, rag retriever.Retriever) *registry.Registry {
	reg := registry.New()

	// 历史 Incident 检索（始终可用）
	if t, err := incidenttool.New(repo).EinoTool(); err == nil {
		_ = reg.Register(t, registry.RiskLow)
	}

	// Runbook 检索（有 RAG 用混合检索，否则词法检索）
	var runbookTool *runbook.Tool
	if rag != nil {
		runbookTool = runbook.NewWithRetriever(rag)
	} else {
		runbookTool = runbook.New("docs")
	}
	if t, err := runbookTool.EinoTool(); err == nil {
		_ = reg.Register(t, registry.RiskLow)
	}

	// Prometheus 指标查询（按配置启用）
	if cfg.Prometheus.URL != "" {
		if t, err := prometheus.New(cfg.Prometheus.URL).EinoTool(); err == nil {
			_ = reg.Register(t, registry.RiskLow)
		}
	}

	// RabbitMQ 队列检查（按配置启用）
	if cfg.RabbitMQ.ManagementURL != "" {
		if t, err := rabbitmq.New(cfg.RabbitMQ.ManagementURL, cfg.RabbitMQ.Username, cfg.RabbitMQ.Password).EinoTool(); err == nil {
			_ = reg.Register(t, registry.RiskLow)
		}
	}

	return reg
}

func loadPrompt(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("load prompt", "error", err, "path", path)
		return "你是运维故障诊断 Agent。"
	}
	return string(data)
}
