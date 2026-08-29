package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/execution/approval"
)

// ApprovalHandler 处理审批查询与批准 / 拒绝。
type ApprovalHandler struct {
	service *approval.Service
}

// NewApprovalHandler 创建审批处理器。
func NewApprovalHandler(svc *approval.Service) *ApprovalHandler {
	return &ApprovalHandler{service: svc}
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
func (h *ApprovalHandler) Approve(c *gin.Context) {
	var req dto.ApprovalActionRequest
	_ = c.ShouldBindJSON(&req)
	who := req.ApprovedBy
	if who == "" {
		who = "anonymous"
	}

	a, err := h.service.Approve(c.Request.Context(), c.Param("id"), who)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

// Reject 处理 POST /api/v1/approvals/:id/reject。
func (h *ApprovalHandler) Reject(c *gin.Context) {
	var req dto.ApprovalActionRequest
	_ = c.ShouldBindJSON(&req)
	who := req.ApprovedBy
	if who == "" {
		who = "anonymous"
	}

	a, err := h.service.Reject(c.Request.Context(), c.Param("id"), who)
	if err != nil {
		h.respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, a)
}

func (h *ApprovalHandler) respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, approval.ErrNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, approval.ErrNotPending):
		c.JSON(http.StatusConflict, dto.ErrorResponse{Error: err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: err.Error()})
	}
}
