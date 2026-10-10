package bootstrap

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/api/middleware"
	"gooncall-agent/internal/api/router"
)

// buildServer 构建 HTTP 引擎。
func (a *App) buildServer() *gin.Engine {
	incidentHandler := handler.NewIncidentHandler(a.incidentSvc).
		WithDiagnoser(a.diagnoser, a.prompt).
		WithPublisher(a)
	// Run 查询接口需要 Incident 仓库做父资源存在性校验（/incidents/:id/runs）。
	runHandler := handler.NewRunHandler(a.runRepo, a.broker).WithIncidentRepository(a.incidentRepo)
	approvalHandler := handler.NewApprovalHandler(a.approvalSvc)
	// AlertManager webhook：创建 Incident 后自动发布 agent.requested（全自动诊断链路）。
	// Run 仓库用于命中去重时判断诊断是否已启动（未启动才允许重新发布）。
	alertHandler := handler.NewAlertHandler(a.incidentSvc).WithPublisher(a).WithRunRepository(a.runRepo)

	engine := router.New(incidentHandler, a.buildHealthHandler(), runHandler, approvalHandler, alertHandler, middleware.AuthConfig{
		WebhookToken: a.cfg.Auth.WebhookToken,
		ReadToken:    a.cfg.Auth.ReadToken,
		AdminToken:   a.cfg.Auth.AdminToken,
		AdminActor:   a.cfg.Auth.AdminActor,
	})
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))
	return engine
}

// buildHealthHandler 装配就绪检查：危险配置、PostgreSQL、RabbitMQ。
//
// 未配置的依赖不注册检查（内存仓库 / 未启用事件时不存在该依赖）。
// 检查失败时 /readyz 返回 503，响应体只给 unavailable，真实原因写日志（可能含 DSN）。
func (a *App) buildHealthHandler() *handler.HealthHandler {
	h := handler.NewHealthHandler().WithCheck("config", func(context.Context) error {
		return a.cfg.Validate()
	})

	if a.dbPing != nil {
		h.WithCheck("postgres", a.dbPing)
	}

	if a.rabbitConn != nil {
		h.WithCheck("rabbitmq", func(context.Context) error {
			if a.rabbitConn.IsClosed() {
				return errors.New("rabbitmq connection is closed")
			}
			return nil
		})
	}

	return h
}
