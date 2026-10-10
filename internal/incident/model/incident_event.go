package model

import (
	"time"

	"github.com/google/uuid"
)

// Incident 生命周期事件类型。
const (
	// EventIncidentCreated 表示 Incident 创建（一次新的告警周期）。
	EventIncidentCreated = "INCIDENT_CREATED"
	// EventStatusTransition 表示 Incident 状态迁移。
	EventStatusTransition = "STATUS_TRANSITION"
	// EventActionCompleted 表示外部动作已明确成功，可安全进入验证阶段。
	EventActionCompleted = "ACTION_COMPLETED"
	// EventActionFailed 表示外部动作明确失败。
	EventActionFailed = "ACTION_FAILED"
	// EventVerificationFailed 表示处置后的连续验证未通过。
	EventVerificationFailed = "VERIFICATION_FAILED"
	// EventPostmortemGenerated 保存已完成闭环的复盘正文与结构化结果。
	EventPostmortemGenerated = "POSTMORTEM_GENERATED"
)

// IncidentEvent 是 Incident 生命周期事件的持久化记录（复用 incident_events 表）。
//
// Payload 至少包含 from / to，reason、error 在非空时写入，CreatedAt 为事件时间。
type IncidentEvent struct {
	ID         string         `json:"id" gorm:"primaryKey;type:varchar(64)"`
	IncidentID string         `json:"incident_id" gorm:"type:varchar(64);not null;index:idx_incident_event_incident"`
	EventType  string         `json:"event_type" gorm:"type:varchar(64);not null"`
	Payload    map[string]any `json:"payload" gorm:"type:jsonb;serializer:json"`
	CreatedAt  time.Time      `json:"created_at" gorm:"not null"`
}

// NewAuditEvent 构造一条带任意 JSON payload 的审计事件。
func NewAuditEvent(incidentID, eventType string, payload map[string]any) *IncidentEvent {
	if payload == nil {
		payload = map[string]any{}
	}
	return &IncidentEvent{
		ID:         "evt_" + uuid.NewString(),
		IncidentID: incidentID,
		EventType:  eventType,
		Payload:    payload,
		CreatedAt:  time.Now(),
	}
}

// TableName 指定 GORM 表名。
func (IncidentEvent) TableName() string { return "incident_events" }

// NewIncidentEvent 构造一条事件：payload 含 from/to，reason、errMsg 非空时一并写入。
func NewIncidentEvent(incidentID, eventType string, from, to Status, reason, errMsg string) *IncidentEvent {
	payload := map[string]any{"from": string(from), "to": string(to)}
	if reason != "" {
		payload["reason"] = reason
	}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	return &IncidentEvent{
		ID:         "evt_" + uuid.NewString(),
		IncidentID: incidentID,
		EventType:  eventType,
		Payload:    payload,
		CreatedAt:  time.Now(),
	}
}

// StatusChange 返回事件载荷中的 from / to（缺失时返回空串）。
func (e *IncidentEvent) StatusChange() (from, to Status) {
	return Status(payloadString(e.Payload, "from")), Status(payloadString(e.Payload, "to"))
}

// ReasonText 返回事件载荷中的 reason，缺失时回退到 error。
func (e *IncidentEvent) ReasonText() string {
	if r := payloadString(e.Payload, "reason"); r != "" {
		return r
	}
	return payloadString(e.Payload, "error")
}

func payloadString(payload map[string]any, key string) string {
	v, _ := payload[key].(string)
	return v
}
