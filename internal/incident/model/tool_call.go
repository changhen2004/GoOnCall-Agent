package model

import "time"

// ToolCall 是一次工具调用（设计文档 5.5）。
type ToolCall struct {
	ID         string    `json:"id" gorm:"primaryKey;type:varchar(64)"`
	RunID      string    `json:"run_id" gorm:"type:varchar(64);not null;index:idx_tool_call_run"`
	ToolName   string    `json:"tool_name" gorm:"type:varchar(128);not null"`
	RiskLevel  string    `json:"risk_level" gorm:"type:varchar(16);not null"`
	Arguments  string    `json:"arguments" gorm:"type:text"`
	Result     string    `json:"result" gorm:"type:text"`
	Status     string    `json:"status" gorm:"type:varchar(32);not null"`
	DurationMs int64     `json:"duration_ms" gorm:"default:0"`
	CreatedAt  time.Time `json:"created_at" gorm:"autoCreateTime"`
}

// TableName 指定 GORM 表名。
func (ToolCall) TableName() string { return "tool_calls" }
