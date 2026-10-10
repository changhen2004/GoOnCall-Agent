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
	ID string `json:"id" gorm:"primaryKey;type:varchar(64)"`
	// IncidentID 上的部分唯一索引保证同一 Incident 最多一条 RUNNING/COMPLETED Run：
	// 重复消息触发第二个 Run 时 INSERT 冲突（ErrConflict），由调用方按"已处理"成功返回；
	// Run 进入 FAILED/INTERRUPTED 后允许重试创建新 Run。
	//
	// 谓词不使用 IN (...) 因为 GORM 索引标签以逗号分隔参数（同 Incident.Fingerprint）。
	IncidentID  string     `json:"incident_id" gorm:"type:varchar(64);not null;index:idx_agent_run_incident;uniqueIndex:idx_agent_run_active,where:status <> 'FAILED' AND status <> 'INTERRUPTED'"`
	AgentType   string     `json:"agent_type" gorm:"type:varchar(64);not null"`
	Status      RunStatus  `json:"status" gorm:"type:varchar(32);not null"`
	Goal        string     `json:"goal" gorm:"type:text"`
	CurrentStep int        `json:"current_step" gorm:"default:0"`
	StartedAt   time.Time  `json:"started_at" gorm:"not null"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Error       string     `json:"error" gorm:"type:text"`
}

// IsActive 判断 Run 是否占用 Incident 的唯一名额（RUNNING/COMPLETED）。
func (r RunStatus) IsActive() bool {
	return r == RunRunning || r == RunCompleted
}

// TableName 指定 GORM 表名。
func (AgentRun) TableName() string { return "agent_runs" }
