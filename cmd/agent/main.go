// 通用 Agent 服务入口：独立端口、独立 SQLite，不复用旧业务 bootstrap。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"

	"gooncall-agent/cmd/internal/knowledgecfg"
	"gooncall-agent/configs"
	agentapi "gooncall-agent/internal/api/agent"
	"gooncall-agent/internal/auth"
	"gooncall-agent/internal/knowledge/rag"
	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/session"
	"gooncall-agent/internal/store"
	"gooncall-agent/internal/tools"
	"gooncall-agent/internal/tools/demo"
	"gooncall-agent/internal/tools/ops"
)

func main() {
	if err := run(); err != nil {
		slog.Error("通用 Agent 服务退出", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	flag.StringVar(&configPath, "config", "", "配置文件路径（默认 configs/config.env）")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	connections, defaultModel, err := cfg.Connections()
	if err != nil {
		return err
	}

	// 先取得独占执行权，再处理旧进程遗留的运行；锁在进程退出后自动释放。
	lock, err := store.Lock(cfg.AgentService.DBPath)
	if err != nil {
		return err
	}
	defer lock.Close()

	db, err := store.Open(cfg.AgentService.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	interruptCtx, cancelInterrupt := context.WithTimeout(context.Background(), 10*time.Second)
	report, err := db.InterruptStale(interruptCtx, time.Now())
	cancelInterrupt()
	if err != nil {
		return fmt.Errorf("处理遗留运行: %w", err)
	}
	if report.Runs > 0 {
		slog.Warn("已中断上次进程遗留的运行",
			"runs", report.Runs,
			"approvals", report.Approvals,
			"tool_executions", report.ToolExecutions)
	}

	registry, err := buildRegistry(cfg)
	if err != nil {
		return err
	}

	authSvc, err := auth.New(db, auth.Options{
		SessionTTL:           cfg.SessionTTL(),
		LoginMaxAttempts:     cfg.Auth.LoginMaxAttempts,
		LoginWindow:          cfg.LoginWindow(),
		LoginLimiterCapacity: cfg.Auth.LoginLimiterCapacity,
	})
	if err != nil {
		return err
	}

	models := make(map[string]session.Model, len(connections))
	for name, connection := range connections {
		models[name] = session.Model{
			Provider:        llm.NewOpenAI(name, connection.BaseURL, connection.APIKey, connection.Model),
			ContextWindow:   connection.ContextWindow,
			MaxOutputTokens: connection.MaxOutputTokens,
		}
	}

	svc, err := session.New(db, session.Options{
		Models:           models,
		DefaultModel:     defaultModel,
		MaxRounds:        cfg.AgentService.MaxRounds,
		RunBudget:        cfg.RunBudget(),
		ApprovalTTL:      cfg.ApprovalTTL(),
		SystemPrompt:     cfg.AgentService.SystemPrompt,
		Registry:         registry,
		ProcessID:        uuid.NewString(),
		Logger:           slog.Default(),
		SubscriberBuffer: cfg.AgentService.SubscriberBuffer,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.AgentService.ListenAddr,
		Handler:           agentapi.New(svc, authSvc, slog.Default()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 禁用用户检测：本地管理命令只写数据库，运行中服务在固定周期内取消其活动运行。
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	go watchDisabledUsers(watchCtx, db, svc, cfg.UserStatusPoll(), slog.Default())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("通用 Agent 服务启动",
		"addr", cfg.AgentService.ListenAddr,
		"db", cfg.AgentService.DBPath,
		"default_model", defaultModel,
		"tools", registry.Names())

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	slog.Info("正在关闭通用 Agent 服务")
	stopWatch()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout())
	defer cancel()

	// 先结束运行（SSE 收到 run_finished 后退出），再关闭 HTTP 服务。
	if err := svc.Shutdown(shutdownCtx); err != nil {
		slog.Warn("等待运行结束超时", "error", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("关闭 HTTP 服务超时", "error", err)
		_ = srv.Close()
	}
	return nil
}

// buildRegistry 只注册显式启用的工具；示例工具与业务工具互不默认混用。
func buildRegistry(cfg *config.Config) (*tools.Registry, error) {
	registry := tools.NewRegistry()
	register := func(registered []tools.Tool) error {
		for _, tool := range registered {
			if err := registry.Register(tool); err != nil {
				return err
			}
		}
		return nil
	}

	if cfg.AgentService.DemoToolsEnabled {
		if err := os.MkdirAll(cfg.AgentService.DemoDir, 0o755); err != nil {
			return nil, fmt.Errorf("创建示例工具目录: %w", err)
		}
		if err := register([]tools.Tool{
			demo.ReadFileTool(cfg.AgentService.DemoDir),
			demo.WriteFileTool(cfg.AgentService.DemoDir),
		}); err != nil {
			return nil, err
		}
	}

	if cfg.Prometheus.Enabled {
		if err := register(ops.NewPrometheusTools(ops.PrometheusOptions{
			URL:            cfg.Prometheus.URL,
			Timeout:        cfg.ToolTimeout(),
			MaxResultBytes: cfg.Tool.MaxResultBytes,
		})); err != nil {
			return nil, err
		}
	}

	if cfg.RabbitMQ.Enabled {
		if err := register([]tools.Tool{ops.NewRabbitMQTool(ops.RabbitMQOptions{
			ManagementURL: cfg.RabbitMQ.ManagementURL,
			Username:      cfg.RabbitMQ.Username,
			Password:      cfg.RabbitMQ.Password,
			Timeout:       cfg.ToolTimeout(),
		})}); err != nil {
			return nil, err
		}
	}

	if cfg.RAG.Enabled {
		// 只校验配置形状；Qdrant/Embedding 在首次检索时连接，不阻断启动。
		builder, err := rag.New(knowledgecfg.From(cfg))
		if err != nil {
			return nil, err
		}
		if err := register([]tools.Tool{ops.NewKnowledgeTool(ops.KnowledgeOptions{
			Provider:       builder.Get,
			TopK:           cfg.RAG.TopK,
			MaxResultBytes: cfg.Tool.MaxResultBytes,
			Timeout:        cfg.ToolTimeout(),
		})}); err != nil {
			return nil, err
		}
	}

	return registry, nil
}

func watchDisabledUsers(ctx context.Context, db *store.DB, svc *session.Service, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		ids, err := db.DisabledUserIDs(ctx)
		if err != nil {
			logger.Error("查询禁用用户失败", "error", err)
			continue
		}
		for _, id := range ids {
			canceled, err := svc.CancelOwnerRuns(ctx, id, "账号已被禁用")
			if err != nil {
				logger.Error("取消禁用用户运行失败", "user_id", id, "error", err)
				continue
			}
			if canceled > 0 {
				logger.Warn("已取消禁用用户的活动运行", "user_id", id, "runs", canceled)
			}
		}
	}
}
