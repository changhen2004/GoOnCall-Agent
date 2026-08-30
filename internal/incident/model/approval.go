package model

import "time"

// ApprovalStatus 是审批状态。审计链：
// PENDING -> APPROVED -> EXECUTING -> EXECUTED / FAILED；PENDING -> REJECTED。
type ApprovalStatus string

const (
	ApprovalPending   ApprovalStatus = "PENDING"
	ApprovalApproved  ApprovalStatus = "APPROVED"
	ApprovalExecuting ApprovalStatus = "EXECUTING"
	ApprovalExecuted  ApprovalStatus = "EXECUTED"
	ApprovalFailed    ApprovalStatus = "FAILED"
	ApprovalRejected  ApprovalStatus = "REJECTED"
)

// Approval 是一条人工审批记录（设计文档 5.6）。
type Approval struct {
	ID         string         `json:"id" gorm:"primaryKey;type:varchar(64)"`
	RunID      string         `json:"run_id" gorm:"type:varchar(64);not null;index:idx_approval_run"`
	ToolCallID string         `json:"tool_call_id" gorm:"type:varchar(64);not null"`
	Action     string         `json:"action" gorm:"type:varchar(128);not null"`
	Arguments  string         `json:"arguments" gorm:"type:text"`
	Reason     string         `json:"reason" gorm:"type:text"`
	Status     ApprovalStatus `json:"status" gorm:"type:varchar(32);not null"`
	ApprovedBy string         `json:"approved_by" gorm:"type:varchar(128)"`
	CreatedAt  time.Time      `json:"created_at" gorm:"autoCreateTime"`
	ApprovedAt *time.Time     `json:"approved_at,omitempty"`
}

// TableName 指定 GORM 表名。
func (Approval) TableName() string { return "approvals" }
