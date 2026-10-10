package model

import "time"

// ApprovalStatus 是审批状态。审计链：
//
//	PENDING -> APPROVED -> EXECUTING -> EXECUTED / FAILED / UNKNOWN
//	PENDING -> REJECTED / EXPIRED
//
// EXPIRED：Incident 已外部恢复或不再允许执行（迁移失败/执行前目标失效）。
// UNKNOWN：外部动作调用超时，无法确认是否已执行；禁止自动再创建新动作。
type ApprovalStatus string

const (
	ApprovalPending   ApprovalStatus = "PENDING"
	ApprovalApproved  ApprovalStatus = "APPROVED"
	ApprovalExecuting ApprovalStatus = "EXECUTING"
	ApprovalExecuted  ApprovalStatus = "EXECUTED"
	ApprovalFailed    ApprovalStatus = "FAILED"
	ApprovalRejected  ApprovalStatus = "REJECTED"
	ApprovalExpired   ApprovalStatus = "EXPIRED"
	// ApprovalUnknown 表示外部动作结果未知（超时），需要人工确认，不允许自动重试。
	ApprovalUnknown ApprovalStatus = "UNKNOWN"
)

// Approval 是一条人工审批记录（设计文档 5.6）。
//
// approval_id 是外部处置的幂等键：执行时写入 HTTP 头 Idempotency-Key。
type Approval struct {
	ID string `json:"id" gorm:"primaryKey;type:varchar(64)"`
	// IncidentID 在创建审批时由 Run 解析并固化，执行时不再反查（外部恢复/重启后仍可判断归属）。
	IncidentID string         `json:"incident_id" gorm:"type:varchar(64);index:idx_approval_incident"`
	RunID      string         `json:"run_id" gorm:"type:varchar(64);not null;index:idx_approval_run"`
	ToolCallID string         `json:"tool_call_id" gorm:"type:varchar(64);not null"`
	Action     string         `json:"action" gorm:"type:varchar(128);not null"`
	Arguments  string         `json:"arguments" gorm:"type:text"`
	Reason     string         `json:"reason" gorm:"type:text"`
	Status     ApprovalStatus `json:"status" gorm:"type:varchar(32);not null"`
	ApprovedBy string         `json:"approved_by" gorm:"type:varchar(128)"`
	// Result / Error / OperationID 记录执行结果，供审计与人工核对。
	Result      string     `json:"result,omitempty" gorm:"type:text"`
	Error       string     `json:"error,omitempty" gorm:"type:text"`
	OperationID string     `json:"operation_id,omitempty" gorm:"type:varchar(255)"`
	CreatedAt   time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
}

// TableName 指定 GORM 表名。
func (Approval) TableName() string { return "approvals" }
