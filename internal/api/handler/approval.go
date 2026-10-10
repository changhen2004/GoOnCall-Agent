package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/api/middleware"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/incident/model"
)

// ApprovalHandler 处理审批查询与批准 / 拒绝。
type ApprovalHandler struct {
	service *approval.Service
}

// NewApprovalHandler 创建审批处理器。
func NewApprovalHandler(svc *approval.Service) *ApprovalHandler {
	return &ApprovalHandler{service: svc}
}

// List 处理 GET /api/v1/approvals?status=PENDING（按 CreatedAt 正序）。
// 只支持 PENDING 过滤：待审批列表是操作者的主要入口。
func (h *ApprovalHandler) List(c *gin.Context) {
	status := c.Query("status")
	if status != "" && status != string(model.ApprovalPending) {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: "unsupported status filter: only PENDING is supported",
		})
		return
	}
	items, err := h.service.ListPending(c.Request.Context())
	if err != nil {
		h.respondError(c, err)
		return
	}
	if items == nil {
		items = []*model.Approval{}
	}
	c.JSON(http.StatusOK, items)
}

// ListByRun 处理 GET /api/v1/runs/:id/approvals（按 CreatedAt 正序）。
// Run 不存在时返回 404，不把"父资源不存在"伪装成空数组。
func (h *ApprovalHandler) ListByRun(c *gin.Context) {
	runID := c.Param("id")
	if _, err := h.service.GetRun(c.Request.Context(), runID); err != nil {
		h.respondError(c, err)
		return
	}
	items, err := h.service.ListByRun(c.Request.Context(), runID)
	if err != nil {
		h.respondError(c, err)
		return
	}
	if items == nil {
		items = []*model.Approval{}
	}
	c.JSON(http.StatusOK, items)
}

// Get 处理 GET /api/v1/approvals/:id。
func (h *ApprovalHandler) Get(c *gin.Context) {
	a, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// Approve 处理 POST /api/v1/approvals/:id/approve。
//
// approved_by 只来自认证上下文（服务端配置的 admin actor）；请求体里的 approved_by
// 一律忽略，避免客户端伪造审批人。
func (h *ApprovalHandler) Approve(c *gin.Context) {
	a, err := h.service.Approve(c.Request.Context(), c.Param("id"), approvalActor(c))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// Reject 处理 POST /api/v1/approvals/:id/reject。
func (h *ApprovalHandler) Reject(c *gin.Context) {
	a, err := h.service.Reject(c.Request.Context(), c.Param("id"), approvalActor(c))
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// approvalActor 返回落库的审批人：认证上下文优先；未启用认证（显式 Demo 配置）时为 anonymous。
func approvalActor(c *gin.Context) string {
	if actor := middleware.Actor(c); actor != "" {
		return actor
	}
	return "anonymous"
}

func (h *ApprovalHandler) respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, approval.ErrNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, approval.ErrNotPending), errors.Is(err, approval.ErrExpired):
		c.JSON(http.StatusConflict, dto.ErrorResponse{Error: err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: err.Error()})
	}
}
