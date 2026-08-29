package model

import "time"

// EvidenceType 是证据类型。
type EvidenceType string

const (
	EvidenceMetric     EvidenceType = "METRIC"
	EvidenceLog        EvidenceType = "LOG"
	EvidenceRunbook    EvidenceType = "RUNBOOK"
	EvidenceHistory    EvidenceType = "HISTORY"
	EvidenceToolResult EvidenceType = "TOOL_RESULT"
	EvidenceDeployment EvidenceType = "DEPLOYMENT"
)

// Evidence 是一条可解释性证据（设计文档 5.4）。
type Evidence struct {
	ID        string         `json:"id" gorm:"primaryKey;type:varchar(64)"`
	RunID     string         `json:"run_id" gorm:"type:varchar(64);index:idx_evidence_run"`
	Type      EvidenceType   `json:"type" gorm:"type:varchar(32);not null"`
	Source    string         `json:"source" gorm:"type:varchar(255)"`
	Content   string         `json:"content" gorm:"type:text"`
	Metadata  map[string]any `json:"metadata" gorm:"type:jsonb;serializer:json"`
	CreatedAt time.Time      `json:"created_at" gorm:"autoCreateTime"`
}

// TableName 指定 GORM 表名。
func (Evidence) TableName() string { return "agent_evidences" }
