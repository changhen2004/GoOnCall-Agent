// Package model 定义 Incident 的核心领域模型。
package model

import (
	"time"
)

// Status 表示 Incident 的生命周期状态。
type Status string

const (
	StatusOpen            Status = "OPEN"
	StatusInvestigating   Status = "INVESTIGATING"
	StatusNeedApproval    Status = "NEED_APPROVAL"
	StatusWaitingApproval Status = "WAITING_APPROVAL"
	StatusMitigating      Status = "MITIGATING"
	StatusVerifying       Status = "VERIFYING"
	StatusResolved        Status = "RESOLVED"
	StatusFailed          Status = "FAILED"
	StatusCancelled       Status = "CANCELLED"
)

// Valid 判断是否为合法状态。
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusInvestigating, StatusNeedApproval, StatusWaitingApproval,
		StatusMitigating, StatusVerifying, StatusResolved, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// IsTerminal 判断是否为终态。
func (s Status) IsTerminal() bool {
	return s == StatusResolved || s == StatusFailed || s == StatusCancelled
}

// Severity 表示告警 / Incident 严重级别。
type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
)

// Incident 是一次服务故障事件。
type Incident struct {
	ID          string `json:"id" gorm:"primaryKey;type:varchar(64)"`
	Service     string `json:"service" gorm:"type:varchar(128);not null;index:idx_incident_service"`
	Severity    string `json:"severity" gorm:"type:varchar(16);not null"`
	Title       string `json:"title" gorm:"type:varchar(255);not null"`
	Description string `json:"description" gorm:"type:text"`
	AlertName   string `json:"alert_name" gorm:"type:varchar(255)"`
	// Fingerprint 的去重唯一性只作用于非终态：同一告警周期只能有一条活动 Incident，
	// 终态（RESOLVED/FAILED/CANCELLED）之后同指纹复发必须能创建新 Incident。
	//
	// WHERE 谓词写成 <> 串联而非 NOT IN (...)，因为 GORM 索引标签以逗号分隔参数，
	// 谓词里的逗号会被截断（schema.parseFieldIndexes）。与 migrations/000004 保持一致。
	Fingerprint string     `json:"fingerprint" gorm:"type:varchar(128);uniqueIndex:idx_incident_active_fingerprint,where:status <> 'RESOLVED' AND status <> 'FAILED' AND status <> 'CANCELLED'"`
	Status      Status     `json:"status" gorm:"type:varchar(32);not null;index:idx_incident_status"`
	StartedAt   time.Time  `json:"started_at" gorm:"not null"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	// Version 乐观锁版本号：更新时 CAS（WHERE version = ?），冲突由 RowsAffected == 0 判定。
	Version int64 `json:"version" gorm:"not null;default:0"`
}

// TableName 指定 GORM 表名。
func (Incident) TableName() string { return "incidents" }
