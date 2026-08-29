package model

// AgentStep 是 Agent 执行过程中的一个步骤（设计文档 5.3）。
type AgentStep struct {
	ID        string `json:"id" gorm:"primaryKey;type:varchar(64)"`
	RunID     string `json:"run_id" gorm:"type:varchar(64);not null;index:idx_agent_step_run"`
	StepIndex int    `json:"step_index" gorm:"not null"`
	Agent     string `json:"agent" gorm:"type:varchar(64);not null"`
	Action    string `json:"action" gorm:"type:varchar(128);not null"`
	Status    string `json:"status" gorm:"type:varchar(32);not null"`
	Input     string `json:"input" gorm:"type:text"`
	Output    string `json:"output" gorm:"type:text"`
	Duration  int64  `json:"duration" gorm:"default:0"`
}

// TableName 指定 GORM 表名。
func (AgentStep) TableName() string { return "agent_steps" }
