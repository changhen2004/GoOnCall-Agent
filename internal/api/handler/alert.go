package handler

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"gooncall-agent/internal/api/dto"
	"gooncall-agent/internal/incident/model"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/observability/metrics"
)

// AlertHandler 接收 Prometheus Alertmanager webhook，创建 / 关闭 Incident。
type AlertHandler struct {
	service   *incidentservice.Service
	publisher Publisher
}

// NewAlertHandler 创建告警处理器。
func NewAlertHandler(svc *incidentservice.Service) *AlertHandler {
	return &AlertHandler{service: svc}
}

// WithPublisher 注入事件发布器：webhook 创建/重开 Incident 后自动发布
// agent.requested（全自动链路：告警即诊断，Worker 消费后进入 INVESTIGATING）。
func (h *AlertHandler) WithPublisher(p Publisher) *AlertHandler {
	h.publisher = p
	return h
}

// Webhook 处理 POST /api/v1/alerts。
// 逐条处理告警：firing -> 创建/重开 Incident 并触发诊断；resolved -> 关闭 Incident。
// 单条失败不再静默吞掉：记录结构化日志、上报指标，并在响应 errors 中返回明细。
func (h *AlertHandler) Webhook(c *gin.Context) {
	var wh dto.AlertWebhook
	if err := c.ShouldBindJSON(&wh); err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	created := make([]string, 0)
	resolved := make([]string, 0)
	failed := make([]string, 0)

	for _, alert := range wh.Alerts {
		service := alertService(alert)
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
			inc, err := h.service.ResolveExternally(c.Request.Context(), fp)
			if err != nil {
				slog.Error("alertmanager webhook resolve incident", "alertname", alertName, "service", service, "error", err)
				metrics.WebhookTotal.WithLabelValues("resolve", "error").Inc()
				failed = append(failed, fmt.Sprintf("resolve %s: %v", alertName, err))
				continue
			}
			metrics.WebhookTotal.WithLabelValues("resolve", "ok").Inc()
			if inc.Status == model.StatusResolved {
				resolved = append(resolved, inc.ID)
			}
			continue
		}

		inc, isNew, err := h.service.Create(c.Request.Context(), incidentservice.CreateIncidentInput{
			Service:     service,
			Severity:    severity,
			Title:       title,
			Description: description,
			AlertName:   alertName,
		})
		if err != nil {
			slog.Error("alertmanager webhook create incident", "alertname", alertName, "service", service, "error", err)
			metrics.WebhookTotal.WithLabelValues("create", "error").Inc()
			failed = append(failed, fmt.Sprintf("create %s: %v", alertName, err))
			continue
		}
		metrics.WebhookTotal.WithLabelValues("create", "ok").Inc()
		if !isNew {
			// 去重命中非终态 Incident（活跃告警周期内重复通知），无需重复建单/诊断。
			continue
		}

		created = append(created, inc.ID)
		if h.publisher != nil {
			if err := h.publisher.PublishAgentRequested(c.Request.Context(), inc.ID); err != nil {
				slog.Error("publish agent.requested", "incident_id", inc.ID, "error", err)
			}
		}
	}

	resp := dto.AlertWebhookResponse{Created: created, Resolved: resolved}
	if len(failed) > 0 {
		resp.Errors = failed
	}
	c.JSON(http.StatusOK, resp)
}

// alertService 解析告警所属服务：优先 labels.service，其次 labels.job，兜底 unknown。
// 服务名参与 Incident 指纹计算，必须跨 firing/resolved 通知保持稳定
// （Alertmanager 的 resolved 通知会携带与 firing 相同的 labels）。
func alertService(alert dto.Alert) string {
	if s := alert.Labels["service"]; s != "" {
		return s
	}
	if s := alert.Labels["job"]; s != "" {
		return s
	}
	return "unknown"
}
