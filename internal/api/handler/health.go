package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// HealthHandler 提供健康检查端点。
type HealthHandler struct{}

// NewHealthHandler 创建健康检查处理器。
func NewHealthHandler() *HealthHandler {
	return &HealthHandler{}
}

// Health 返回服务存活状态。
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready 返回服务就绪状态。
func (h *HealthHandler) Ready(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
