// Package bootstrap 负责应用装配（config/database/knowledge/tools/messaging/agent/server）。
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/messaging"
	"gooncall-agent/internal/tool/deployment"
	"gooncall-agent/internal/tool/registry"
)

// App 是装配完成的应用。
type App struct {
	cfg         *config.Config
	engine      *gin.Engine
	incidentSvc *incidentservice.Service
	runRepo     repository.RunRepository
	approvalSvc *approval.Service
	broker      *agentruntime.StreamBroker
	producer    *messaging.Producer
	agentRT     *agentruntime.Runtime
	diagnoser   handler.Diagnoser
	prompt      string
}

// New 装配整个应用。
func New() (*App, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	dbs, err := buildDatabases(cfg)
	if err != nil {
		return nil, err
	}

	app := &App{
		cfg:         cfg,
		incidentSvc: incidentservice.New(dbs.incidentRepo),
		runRepo:     dbs.runRepo,
		broker:      agentruntime.NewStreamBroker(),
	}

	var restartTool *deployment.Tool
	app.approvalSvc, restartTool = buildExecution(cfg, app.incidentSvc, dbs.runRepo, dbs.approvalRepo, app.broker)

	ragRetriever, err := buildRetriever(cfg)
	if err != nil {
		return nil, err
	}
	reg := buildToolRegistry(cfg, dbs.incidentRepo, ragRetriever)
	if t, err := restartTool.EinoTool(); err == nil {
		_ = reg.Register(t, registry.RiskMedium)
	}

	app.agentRT, app.diagnoser, app.prompt = buildAgent(cfg, reg, dbs.runRepo, app.broker)

	app.producer, err = buildProducer(cfg)
	if err != nil {
		return nil, err
	}

	app.engine = app.buildServer()
	return app, nil
}

// Run 启动 HTTP 服务。
func (a *App) Run() error {
	addr := fmt.Sprintf("%s:%d", a.cfg.Server.Host, a.cfg.Server.Port)
	slog.Info("starting gooncall api", "addr", addr)
	return a.engine.Run(addr)
}

// RunWorker 启动事件消费者（RabbitMQ -> Worker -> Agent）。
func (a *App) RunWorker(ctx context.Context) error {
	if a.cfg.RabbitMQ.URL == "" {
		return fmt.Errorf("RABBITMQ_URL is empty, worker cannot start")
	}
	conn, err := messaging.Connect(a.cfg.RabbitMQ.URL)
	if err != nil {
		return fmt.Errorf("connect rabbitmq: %w", err)
	}
	defer conn.Close()

	consumer := messaging.NewConsumer(conn, "gooncall.agent.queue")
	slog.Info("gooncall worker started", "routing_key", messaging.AgentRequested)
	return consumer.Subscribe(ctx, messaging.AgentRequested, a.handleAgentRequested)
}

// PublishAgentRequested 发布 agent.requested 事件（实现 handler.Publisher）。
func (a *App) PublishAgentRequested(ctx context.Context, incidentID string) error {
	if a.producer == nil {
		return nil
	}
	ev, err := messaging.NewEvent(messaging.AgentRequested, map[string]string{"incident_id": incidentID})
	if err != nil {
		return err
	}
	return a.producer.Publish(ctx, messaging.AgentRequested, ev)
}

// handleAgentRequested 处理 agent.requested：分析并运行诊断 Agent。
func (a *App) handleAgentRequested(ctx context.Context, event messaging.Event) error {
	var payload struct {
		IncidentID string `json:"incident_id"`
	}
	if err := event.DecodePayload(&payload); err != nil {
		return err
	}
	slog.Info("agent.requested", "event_id", event.ID, "incident_id", payload.IncidentID)

	inc, err := a.incidentSvc.Get(ctx, payload.IncidentID)
	if err != nil {
		return err
	}
	if inc.Status == model.StatusOpen {
		if inc, err = a.incidentSvc.Analyze(ctx, inc.ID); err != nil {
			return err
		}
	}
	if a.agentRT == nil {
		slog.Warn("agent runtime not configured, skip diagnosis", "incident_id", inc.ID)
		return nil
	}
	_, err = a.agentRT.Diagnose(ctx, inc, a.prompt)
	return err
}
