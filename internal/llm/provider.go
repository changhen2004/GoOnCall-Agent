// Package llm 定义单次模型调用的契约，并提供 OpenAI 兼容实现。
//
// 这一层只负责一次请求：不保存会话、不执行工具、不做重试或自动路由。
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// Role 是消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// FunctionCall 是模型返回的工具调用参数。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall 是一次工具调用。ID 只在当前运行内关联工具结果。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionDef 是提供给模型的工具定义。
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Tool 是 OpenAI tools 数组元素。
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// Message 是传给模型或从模型收到的消息。
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Usage 是上游返回的 token 用量（不保证所有兼容服务都提供）。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Request 是一次完整的模型请求。Model 为空时使用连接自身的模型名。
type Request struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	MaxTokens   int
	Temperature *float64
}

// Response 是普通调用的完整结果。
type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        *Usage
}

// ToolCallDelta 是流式工具调用的一个分片。
type ToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

// StreamChunk 是一次流式增量。文本增量用于实时展示，不逐片写库。
type StreamChunk struct {
	Content      string
	ToolCalls    []ToolCallDelta
	FinishReason string
	Usage        *Usage
}

// Stream 表示一次 SSE 响应。
//
// Recv 返回 io.EOF 表示流正常结束；此时调用 Response 获取组装后的完整结果。
// Response 会校验工具调用是否完整、参数是否为有效 JSON。
type Stream interface {
	Recv() (StreamChunk, error)
	Response() (*Response, error)
	Close() error
}

// Provider 是 OpenAI 兼容模型连接的统一抽象。
type Provider interface {
	Name() string
	Chat(ctx context.Context, req Request) (*Response, error)
	ChatStream(ctx context.Context, req Request) (Stream, error)
}

var (
	// ErrUpstream 表示上游服务明确返回错误。
	ErrUpstream = errors.New("模型服务返回错误")
	// ErrIncomplete 表示响应不完整，不能当作成功结果使用。
	ErrIncomplete = errors.New("模型响应不完整")
)
