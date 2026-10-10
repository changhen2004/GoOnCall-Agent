package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
)

// RunHandler 处理 Agent Run 查询与 SSE 流。
type RunHandler struct {
	repo      repository.RunRepository
	broker    *agentruntime.StreamBroker
	incidents repository.Repository
}

// NewRunHandler 创建 Run 处理器。
func NewRunHandler(repo repository.RunRepository, broker *agentruntime.StreamBroker) *RunHandler {
	return &RunHandler{repo: repo, broker: broker}
}

// Get 处理 GET /api/v1/runs/:id。
func (h *RunHandler) Get(c *gin.Context) {
	run, err := h.repo.GetRun(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

// Steps 处理 GET /api/v1/runs/:id/steps。
func (h *RunHandler) Steps(c *gin.Context) {
	steps, err := h.repo.ListSteps(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, steps)
}

// ToolCalls 处理 GET /api/v1/runs/:id/tool-calls（按 CreatedAt 正序）。
// Run 不存在时返回 404，不把"父资源不存在"伪装成空数组。
func (h *RunHandler) ToolCalls(c *gin.Context) {
	runID := c.Param("id")
	if _, err := h.repo.GetRun(c.Request.Context(), runID); err != nil {
		respondError(c, err)
		return
	}
	calls, err := h.repo.ListToolCalls(c.Request.Context(), runID)
	if err != nil {
		respondError(c, err)
		return
	}
	if calls == nil {
		calls = []*model.ToolCall{}
	}
	c.JSON(http.StatusOK, calls)
}

// RunsByIncident 处理 GET /api/v1/incidents/:id/runs（按 StartedAt 倒序）。
// 父资源（Incident）不存在时返回 404。
func (h *RunHandler) RunsByIncident(c *gin.Context) {
	incidentID := c.Param("id")
	if h.incidents == nil {
		c.JSON(http.StatusServiceUnavailable, dto.ErrorResponse{Error: "incident repository is not configured"})
		return
	}
	if _, err := h.incidents.GetByID(c.Request.Context(), incidentID); err != nil {
		respondError(c, err)
		return
	}
	runs, err := h.repo.ListRunsByIncident(c.Request.Context(), incidentID)
	if err != nil {
		respondError(c, err)
		return
	}
	if runs == nil {
		runs = []*model.AgentRun{}
	}
	c.JSON(http.StatusOK, runs)
}

// WithIncidentRepository 注入 Incident 仓库，用于 /incidents/:id/runs 的父资源存在性校验。
func (h *RunHandler) WithIncidentRepository(repo repository.Repository) *RunHandler {
	h.incidents = repo
	return h
}

// Evidences 处理 GET /api/v1/runs/:id/evidences。
func (h *RunHandler) Evidences(c *gin.Context) {
	evs, err := h.repo.ListEvidences(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, evs)
}

// Stream 处理 GET /api/v1/runs/:id/stream（SSE）。
func (h *RunHandler) Stream(c *gin.Context) {
	runID := c.Param("id")
	if _, err := h.repo.GetRun(c.Request.Context(), runID); err != nil {
		respondError(c, err)
		return
	}

	ch, cancel := h.broker.Subscribe(runID)
	defer cancel()

	c.Stream(func(w io.Writer) bool {
		select {
		case ev, ok := <-ch:
			if !ok {
				return false
			}
			data, _ := json.Marshal(ev.Data)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}
