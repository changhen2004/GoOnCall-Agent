// Package router 装配 HTTP 路由。
package router

import (
	"github.com/gin-gonic/gin"

	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/api/middleware"
)

// New 构建 Gin 引擎并注册路由。
//
// auth 中配置了 Token 的路由分组才安装认证中间件；三个 Token 全空时不做认证
// （仅限显式 Demo 配置：execution.mode=mock，见 config.Validate）。
//
// 权限划分（契约 3.3）：
//   - webhook：POST /alerts，使用 X-GoOnCall-Webhook-Token；
//   - read：只读查询，read token 或 admin token（admin 具备读权限）；
//   - admin：Incident 写操作与审批，只接受 admin token。
func New(
	incidentHandler *handler.IncidentHandler,
	healthHandler *handler.HealthHandler,
	runHandler *handler.RunHandler,
	approvalHandler *handler.ApprovalHandler,
	alertHandler *handler.AlertHandler,
	auth middleware.AuthConfig,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), middleware.Logger())

	r.GET("/healthz", healthHandler.Health)
	r.GET("/readyz", healthHandler.Ready)

	v1 := r.Group("/api/v1")

	read := v1.Group("")
	if auth.Enabled() {
		read.Use(middleware.ReadAuth(auth))
	}
	{
		read.GET("/incidents", incidentHandler.List)
		read.GET("/incidents/:id", incidentHandler.Get)
		// 持久化查询：不依赖 SSE，重启后仍可读到完整链路。
		read.GET("/incidents/:id/runs", runHandler.RunsByIncident)
		read.GET("/incidents/:id/events", incidentHandler.Events)

		read.GET("/runs/:id", runHandler.Get)
		read.GET("/runs/:id/steps", runHandler.Steps)
		read.GET("/runs/:id/evidences", runHandler.Evidences)
		read.GET("/runs/:id/tool-calls", runHandler.ToolCalls)
		read.GET("/runs/:id/approvals", approvalHandler.ListByRun)
		// SSE 是单进程实验能力，多副本部署时事件不跨进程同步。
		read.GET("/runs/:id/stream", runHandler.Stream)

		read.GET("/approvals", approvalHandler.List)
		read.GET("/approvals/:id", approvalHandler.Get)
	}

	webhook := v1.Group("")
	if auth.Enabled() {
		webhook.Use(middleware.WebhookAuth(auth))
	}
	webhook.POST("/alerts", alertHandler.Webhook)

	admin := v1.Group("")
	if auth.Enabled() {
		admin.Use(middleware.AdminAuth(auth))
	}
	{
		admin.POST("/incidents", incidentHandler.Create)
		admin.POST("/incidents/:id/analyze", incidentHandler.Analyze)
		admin.POST("/incidents/:id/resolve", incidentHandler.Resolve)
		admin.POST("/approvals/:id/approve", approvalHandler.Approve)
		admin.POST("/approvals/:id/reject", approvalHandler.Reject)
	}

	return r
}
