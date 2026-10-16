// Package bootstrap 负责应用装配（config/database/knowledge/tools/messaging/agent/server）。
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/messaging"
	"gooncall-agent/internal/observability/metrics"
	"gooncall-agent/internal/tool/registry"
)

// App 是装配完成的应用。
type App struct {
	cfg          *config.Config
	engine       *gin.Engine
	incidentSvc  *incidentservice.Service
	incidentRepo repository.Repository
	approvalRepo repository.ApprovalRepository
	runRepo      repository.RunRepository
	approvalSvc  *approval.Service
	broker       *agentruntime.StreamBroker
	producer     *messaging.Producer
	agentRT      *agentruntime.Runtime
	diagnoser    handler.Diagnoser
	prompt       string
	// dbPing / rabbitConn 只用于就绪检查；未配置时为 nil（内存仓库 / 未启用事件）。
	dbPing     func(ctx context.Context) error
	rabbitConn *amqp.Connection
}

// New 装配整个应用。
func New() (*App, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	ragRetriever, err := buildRetriever(cfg)
	if err != nil {
		return nil, err
	}

	dbs, err := buildDatabases(cfg)
	if err != nil {
		return nil, err
	}

	app := &App{
		cfg:          cfg,
		incidentSvc:  incidentservice.New(dbs.incidentRepo),
		incidentRepo: dbs.incidentRepo,
		approvalRepo: dbs.approvalRepo,
		runRepo:      dbs.runRepo,
		broker:       agentruntime.NewStreamBroker(),
	}

	if dbs.db != nil {
		app.dbPing = pingPostgres(dbs.db)
	}

	restartTool, err := buildExecution(cfg, app)
	if err != nil {
		return nil, err
	}

	reg := buildToolRegistry(cfg, dbs.incidentRepo, ragRetriever)
	if t, err := restartTool.EinoTool(); err == nil {
		_ = reg.Register(t, registry.RiskMedium)
	}

	app.agentRT, app.diagnoser, app.prompt = buildAgent(cfg, reg, dbs.runRepo, app.broker)

	app.producer, app.rabbitConn, err = buildProducer(cfg)
	if err != nil {
		return nil, err
	}

	if err := app.registerStatusCollectors(); err != nil {
		return nil, err
	}

	app.engine = app.buildServer()
	return app, nil
}

// pingPostgres 返回 PostgreSQL 就绪检查（复用已建立的连接池）。
func pingPostgres(db *gorm.DB) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.PingContext(ctx)
	}
}

// registerStatusCollectors 注册"各状态 Incident / 审批数"指标。
//
// 用 Prometheus 采集器在抓取时现算：不需要后台轮询或定时器，指标不会过期。
func (a *App) registerStatusCollectors() error {
	err := metrics.Register(metrics.NewStatusCollector(
		"gooncall_incidents",
		"Number of incidents by status.",
		"status",
		func(ctx context.Context) (map[string]int, error) {
			items, err := a.incidentSvc.List(ctx, repository.ListFilter{})
			if err != nil {
				return nil, err
			}
			counts := make(map[string]int, len(items))
			for _, inc := range items {
				counts[string(inc.Status)]++
			}
			return counts, nil
		},
	))
	if err != nil {
		return fmt.Errorf("register incident status collector: %w", err)
	}

	err = metrics.Register(metrics.NewStatusCollector(
		"gooncall_approvals",
		"Number of approvals by status.",
		"status",
		func(ctx context.Context) (map[string]int, error) {
			if a.approvalSvc == nil {
				return nil, nil
			}
			pending, err := a.approvalSvc.ListPending(ctx)
			if err != nil {
				return nil, err
			}
			counts := map[string]int{string(model.ApprovalPending): len(pending)}
			return counts, nil
		},
	))
	if err != nil {
		return fmt.Errorf("register approval status collector: %w", err)
	}
	return nil
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

	consumer := messaging.NewConsumer(conn, messaging.QueueAgent).
		WithTerminalHandler(a.failIncidentOnDeadLetter)
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

// handleAgentRequested 处理 agent.requested：交给 RunDiagnosis 的幂等入口。
//
// 处理失败返回错误，由 Consumer 路由到重试队列（并计入重试指标）；重试耗尽后由
// failIncidentOnDeadLetter 结束 Incident。
func (a *App) handleAgentRequested(ctx context.Context, event messaging.Event) error {
	var payload struct {
		IncidentID string `json:"incident_id"`
	}
	if err := event.DecodePayload(&payload); err != nil {
		return err
	}
	slog.Info("agent.requested", "event_id", event.ID, "incident_id", payload.IncidentID)

	if err := RunDiagnosis(ctx, a.incidentSvc, a.runRepo, a.agentRT, a.prompt, payload.IncidentID); err != nil {
		return err
	}
	return nil
}

// failIncidentOnDeadLetter 是重试耗尽的终态回调：消息即将进入 DLQ 时把 Incident 置为 FAILED
// 并记录最后错误，避免告警永久停在非终态。
func (a *App) failIncidentOnDeadLetter(ctx context.Context, event messaging.Event, cause error) error {
	var payload struct {
		IncidentID string `json:"incident_id"`
	}
	if err := event.DecodePayload(&payload); err != nil || payload.IncidentID == "" {
		// 无法定位 Incident：只进 DLQ，不改状态。
		return nil
	}
	if _, err := a.incidentSvc.Fail(ctx, payload.IncidentID, "agent event retries exhausted", cause.Error()); err != nil {
		if errors.Is(err, incidentservice.ErrInvalidTransition) {
			// 已终态或已进入处置流程：无需改动，不算失败。
			slog.Info("incident already terminal, skip dead letter finalization", "incident_id", payload.IncidentID)
			return nil
		}
		return err
	}
	slog.Warn("incident marked FAILED after retries exhausted", "incident_id", payload.IncidentID)
	return nil
}
