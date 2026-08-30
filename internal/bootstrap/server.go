package bootstrap

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/api/router"
)

// buildServer 构建 HTTP 引擎。
func (a *App) buildServer() *gin.Engine {
	incidentHandler := handler.NewIncidentHandler(a.incidentSvc).
		WithDiagnoser(a.diagnoser, a.prompt).
		WithPublisher(a)
	runHandler := handler.NewRunHandler(a.runRepo, a.broker)
	approvalHandler := handler.NewApprovalHandler(a.approvalSvc)
	alertHandler := handler.NewAlertHandler(a.incidentSvc)

	engine := router.New(incidentHandler, handler.NewHealthHandler(), runHandler, approvalHandler, alertHandler)
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))
	return engine
}
