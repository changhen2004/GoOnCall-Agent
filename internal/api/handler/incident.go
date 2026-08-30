package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
)

// Diagnoser 执行 Incident 诊断，由 Agent Runtime 实现。
type Diagnoser interface {
	Diagnose(ctx context.Context, inc *model.Incident, systemPrompt string) (*agentruntime.DiagnosisResult, error)
}

// Publisher 发布 Agent 请求事件（异步诊断）。
type Publisher interface {
	PublishAgentRequested(ctx context.Context, incidentID string) error
}

// IncidentHandler 处理 Incident 相关 HTTP 请求。
type IncidentHandler struct {
	service         *incidentservice.Service
	diagnoser       Diagnoser
	diagnosisPrompt string
	publisher       Publisher
}

// NewIncidentHandler 创建 Incident 处理器。
func NewIncidentHandler(svc *incidentservice.Service) *IncidentHandler {
	return &IncidentHandler{service: svc}
}

// WithDiagnoser 注入诊断 Agent（同步兜底；未配置时 analyze 仅做状态流转）。
func (h *IncidentHandler) WithDiagnoser(d Diagnoser, prompt string) *IncidentHandler {
	h.diagnoser = d
	h.diagnosisPrompt = prompt
	return h
}

// WithPublisher 注入事件发布器（异步诊断：RabbitMQ -> Worker -> Agent）。
func (h *IncidentHandler) WithPublisher(p Publisher) *IncidentHandler {
	h.publisher = p
	return h
}

// Create 处理 POST /api/v1/incidents。
func (h *IncidentHandler) Create(c *gin.Context) {
	var req dto.CreateIncidentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	inc, created, err := h.service.Create(c.Request.Context(), incidentservice.CreateIncidentInput{
		Service:     req.Service,
		Severity:    req.Severity,
		Title:       req.Title,
		Description: req.Description,
		AlertName:   req.AlertName,
	})
	if err != nil {
		respondError(c, err)
		return
	}

	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	c.JSON(status, dto.ToIncidentResponse(inc))
}

// List 处理 GET /api/v1/incidents。
func (h *IncidentHandler) List(c *gin.Context) {
	filter := repository.ListFilter{
		Service: c.Query("service"),
		Status:  model.Status(c.Query("status")),
		Limit:   parseIntDefault(c.Query("limit"), 50),
		Offset:  parseIntDefault(c.Query("offset"), 0),
	}

	items, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		respondError(c, err)
		return
	}

	c.JSON(http.StatusOK, dto.ListIncidentsResponse{
		Items: dto.ToIncidentResponses(items),
		Total: len(items),
	})
}

// Get 处理 GET /api/v1/incidents/:id。
func (h *IncidentHandler) Get(c *gin.Context) {
	inc, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.ToIncidentResponse(inc))
}

// Analyze 处理 POST /api/v1/incidents/:id/analyze。
// 先执行状态流转（OPEN -> INVESTIGATING），再（可选）运行诊断 Agent。
func (h *IncidentHandler) Analyze(c *gin.Context) {
	inc, err := h.service.Analyze(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}

	resp := dto.AnalyzeResponse{Incident: dto.ToIncidentResponse(inc)}

	// 优先异步（RabbitMQ -> Worker -> Agent），否则同步诊断兜底。
	if h.publisher != nil {
		if err := h.publisher.PublishAgentRequested(c.Request.Context(), inc.ID); err != nil {
			resp.Diagnosis = &dto.DiagnosisResponse{Error: err.Error()}
		}
	} else if h.diagnoser != nil {
		result, err := h.diagnoser.Diagnose(c.Request.Context(), inc, h.diagnosisPrompt)
		if err != nil {
			resp.Diagnosis = &dto.DiagnosisResponse{Error: err.Error()}
		} else {
			resp.Diagnosis = &dto.DiagnosisResponse{RunID: result.RunID, Conclusion: result.Conclusion}
		}
	}

	c.JSON(http.StatusOK, resp)
}

// Resolve 处理 POST /api/v1/incidents/:id/resolve。
func (h *IncidentHandler) Resolve(c *gin.Context) {
	inc, err := h.service.Resolve(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.ToIncidentResponse(inc))
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, incidentservice.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, incidentservice.ErrNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, incidentservice.ErrInvalidTransition),
		errors.Is(err, incidentservice.ErrConcurrentModification):
		c.JSON(http.StatusConflict, dto.ErrorResponse{Error: err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: err.Error()})
	}
}
