// Package model 定义知识库文档片段（Chunk）模型。
package model

// Chunk 是知识文档的一个检索片段。
type Chunk struct {
	ID       string   `json:"id"`
	Source   string   `json:"source"`
	DocType  string   `json:"doc_type"`
	Service  string   `json:"service"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Severity string   `json:"severity"`
	Tags     []string `json:"tags"`
}
