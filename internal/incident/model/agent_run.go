package model

import "time"

// RunStatus 是 Agent Run 的生命周期状态。
type RunStatus string

const (
	RunRunning     RunStatus = "RUNNING"
	RunCompleted   RunStatus = "COMPLETED"
	RunFailed      RunStatus = "FAILED"
	RunInterrupted RunStatus = "INTERRUPTED"
)

// AgentRun 是一次 Agent 执行（设计文档 5.2）。
type AgentRun struct {
	ID          string     `json:"id" gorm:"primaryKey;type:varchar(64)"`
	IncidentID  string     `json:"incident_id" gorm:"type:varchar(64);not null;index:idx_agent_run_incident"`
	AgentType   string     `json:"agent_type" gorm:"type:varchar(64);not null"`
	Status      RunStatus  `json:"status" gorm:"type:varchar(32);not null"`
	Goal        string     `json:"goal" gorm:"type:text"`
	CurrentStep int        `json:"current_step" gorm:"default:0"`
	StartedAt   time.Time  `json:"started_at" gorm:"not null"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error" gorm:"type:text"`
}

// TableName 指定 GORM 表名。
func (AgentRun) TableName() string { return "agent_runs" }
