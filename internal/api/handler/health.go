package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// readinessTimeout 是所有就绪检查共享的总超时。
const readinessTimeout = 3 * time.Second

// CheckFunc 是一个就绪检查：返回 nil 表示依赖可用。
type CheckFunc func(ctx context.Context) error

type namedCheck struct {
	name string
	fn   CheckFunc
}

// HealthHandler 提供存活与就绪检查。
type HealthHandler struct {
	checks []namedCheck
}

// NewHealthHandler 创建健康检查处理器。
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// WithCheck 追加一个就绪检查（按注册顺序执行与输出，便于定位）。
func (h *HealthHandler) WithCheck(name string, fn CheckFunc) *HealthHandler {
	h.checks = append(h.checks, namedCheck{name: name, fn: fn})
	return h
}

// Health 返回服务存活状态（进程活着即 200，不检查依赖）。
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready 返回服务就绪状态：任一依赖不可用或配置危险时返回 503。
//
// 响应体只给出每个检查的 ok/unavailable：依赖错误原文可能包含 DSN、主机或凭证，
// 详细原因由 Bootstrap 记录到日志。
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
	defer cancel()

	checks := make(gin.H, len(h.checks))
	ready := true
	for _, check := range h.checks {
		if err := check.fn(ctx); err != nil {
			slog.Warn("readiness check failed", "check", check.name, "error", err)
			ready = false
			checks[check.name] = "unavailable"
			continue
		}
		checks[check.name] = "ok"
	}

	if !ready {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "checks": checks})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
}
