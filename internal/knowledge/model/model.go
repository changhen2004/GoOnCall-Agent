// Package model 定义知识库文档片段（Chunk）模型。
package model

// Chunk 是知识文档的一个检索片段。
type Chunk struct {
	ID             string   `json:"id"`
	DocID          string   `json:"doc_id"`
	Source         string   `json:"source"`
	DocType        string   `json:"doc_type"`
	Service        string   `json:"service"`
	Component      string   `json:"component"`
	IncidentType   string   `json:"incident_type"`
	ProductVersion string   `json:"product_version"`
	SourceKind     string   `json:"source_kind"`
	SourceURL      string   `json:"source_url"`
	LastVerifiedAt string   `json:"last_verified_at"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	Severity       string   `json:"severity"`
	Tags           []string `json:"tags"`
}
