package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/incident/repository"
)

// RunHandler 处理 Agent Run 查询与 SSE 流。
type RunHandler struct {
	repo   repository.RunRepository
	broker *agentruntime.StreamBroker
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
