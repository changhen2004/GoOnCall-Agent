package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/incident/model"
	incidentservice "gooncall-agent/internal/incident/service"
)

// AlertHandler 接收 Prometheus Alertmanager webhook，创建 / 关闭 Incident。
type AlertHandler struct {
	service *incidentservice.Service
}

// NewAlertHandler 创建告警处理器。
func NewAlertHandler(svc *incidentservice.Service) *AlertHandler {
	return &AlertHandler{service: svc}
}

// Webhook 处理 POST /api/v1/alerts。
func (h *AlertHandler) Webhook(c *gin.Context) {
	var wh dto.AlertWebhook
	if err := c.ShouldBindJSON(&wh); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	created := make([]string, 0)
	resolved := make([]string, 0)

	for _, alert := range wh.Alerts {
		service := alert.Labels["service"]
		alertName := alert.Labels["alertname"]
		title := alert.Annotations["summary"]
		if title == "" {
			title = alertName
		}
		if title == "" {
			title = "unknown alert"
		}
		description := alert.Annotations["description"]
		severity := alert.Labels["severity"]

		if alert.Status == "resolved" {
			fp := model.Fingerprint(service, alertName, title)
			if inc, err := h.service.ResolveByFingerprint(c.Request.Context(), fp); err == nil && inc.Status == model.StatusResolved {
				resolved = append(resolved, inc.ID)
			}
		} else {
			inc, isNew, err := h.service.Create(c.Request.Context(), incidentservice.CreateIncidentInput{
				Service:     service,
				Severity:    severity,
				Title:       title,
				Description: description,
				AlertName:   alertName,
			})
			if err == nil && isNew {
				created = append(created, inc.ID)
			}
		}
	}

	c.JSON(http.StatusOK, dto.AlertWebhookResponse{Created: created, Resolved: resolved})
}
