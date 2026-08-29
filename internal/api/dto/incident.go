// Package dto 定义 API 请求 / 响应传输对象。
package dto

import (
	"time"

	"gooncall-agent/internal/incident/model"
)

// CreateIncidentRequest 是创建 Incident 的请求体。
type CreateIncidentRequest struct {
	Service     string `json:"service" binding:"required"`
	Severity    string `json:"severity"`
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	AlertName   string `json:"alert_name"`
}

// IncidentResponse 是 Incident 的响应体。
type IncidentResponse struct {
	ID          string     `json:"id"`
	Service     string     `json:"service"`
	Severity    string     `json:"severity"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	AlertName   string     `json:"alert_name"`
	Fingerprint string     `json:"fingerprint"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ListIncidentsResponse 是 Incident 列表响应体。
type ListIncidentsResponse struct {
	Items []IncidentResponse `json:"items"`
	Total int                `json:"total"`
}

// ErrorResponse 是统一错误响应体。
type ErrorResponse struct {
	Error string `json:"error"`
}

// ToIncidentResponse 将领域模型转换为响应体。
func ToIncidentResponse(inc *model.Incident) IncidentResponse {
	return IncidentResponse{
		ID:          inc.ID,
		Service:     inc.Service,
		Severity:    inc.Severity,
		Title:       inc.Title,
		Description: inc.Description,
		AlertName:   inc.AlertName,
		Fingerprint: inc.Fingerprint,
		Status:      string(inc.Status),
		StartedAt:   inc.StartedAt,
		ResolvedAt:  inc.ResolvedAt,
		CreatedAt:   inc.CreatedAt,
		UpdatedAt:   inc.UpdatedAt,
	}
}

// ToIncidentResponses 批量转换。
func ToIncidentResponses(incs []*model.Incident) []IncidentResponse {
	out := make([]IncidentResponse, 0, len(incs))
	for _, inc := range incs {
		out = append(out, ToIncidentResponse(inc))
	}
	return out
}

// DiagnosisResponse 是诊断结果响应体。
type DiagnosisResponse struct {
	RunID      string `json:"run_id,omitempty"`
	Conclusion string `json:"conclusion"`
	Error      string `json:"error,omitempty"`
}

// AnalyzeResponse 是 analyze 端点响应体：事件状态 + 诊断结论。
type AnalyzeResponse struct {
	Incident  IncidentResponse   `json:"incident"`
	Diagnosis *DiagnosisResponse `json:"diagnosis,omitempty"`
}
