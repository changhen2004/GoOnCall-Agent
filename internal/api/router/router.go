// Package router 装配 HTTP 路由。
package router

import (
	"github.com/gin-gonic/gin"

	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/api/middleware"
)

// New 构建 Gin 引擎并注册路由。
func New(incidentHandler *handler.IncidentHandler, healthHandler *handler.HealthHandler, runHandler *handler.RunHandler, approvalHandler *handler.ApprovalHandler, alertHandler *handler.AlertHandler) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), middleware.Logger())

	r.GET("/healthz", healthHandler.Health)
	r.GET("/readyz", healthHandler.Ready)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/incidents", incidentHandler.Create)
		v1.GET("/incidents", incidentHandler.List)
		v1.GET("/incidents/:id", incidentHandler.Get)
		v1.POST("/incidents/:id/analyze", incidentHandler.Analyze)
		v1.POST("/incidents/:id/resolve", incidentHandler.Resolve)

		v1.POST("/alerts", alertHandler.Webhook)

		v1.GET("/runs/:id", runHandler.Get)
		v1.GET("/runs/:id/steps", runHandler.Steps)
		v1.GET("/runs/:id/evidences", runHandler.Evidences)
		v1.GET("/runs/:id/stream", runHandler.Stream)

		v1.GET("/approvals/:id", approvalHandler.Get)
		v1.POST("/approvals/:id/approve", approvalHandler.Approve)
		v1.POST("/approvals/:id/reject", approvalHandler.Reject)
	}

	return r
}
