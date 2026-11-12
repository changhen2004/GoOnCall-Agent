package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAI 是 OpenAI 兼容 /chat/completions 客户端。
//
// base_url 约定：包含版本前缀（例如 https://api.openai.com/v1），
// 客户端只在末尾拼接 /chat/completions，不会自动补 /v1。
type OpenAI struct {
	name    string
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewOpenAI 创建模型连接。
func NewOpenAI(name, baseURL, apiKey, model string) *OpenAI {
	return &OpenAI{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		client:  &http.Client{},
	}
}

// Name 返回连接名。
func (o *OpenAI) Name() string { return o.name }

// Chat 发起一次非流式请求。
func (o *OpenAI) Chat(ctx context.Context, req Request) (*Response, error) {
	resp, err := o.post(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out openAIResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析模型响应: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%w: %s", ErrUpstream, out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%w: 响应中没有 choices", ErrIncomplete)
	}

	choice := out.Choices[0]
	calls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		if call.Type == "" {
			call.Type = "function"
		}
		if call.Function.Arguments == "" {
			call.Function.Arguments = "{}"
		}
		if !json.Valid([]byte(call.Function.Arguments)) {
			return nil, fmt.Errorf("%w: 工具 %s 的参数不是有效 JSON", ErrIncomplete, call.Function.Name)
		}
		calls = append(calls, call)
	}

	return &Response{
		Content:      choice.Message.Content,
		ToolCalls:    calls,
		FinishReason: choice.FinishReason,
		Usage:        out.Usage,
	}, nil
}

// ChatStream 发起一次真实 SSE 请求。
func (o *OpenAI) ChatStream(ctx context.Context, req Request) (Stream, error) {
	resp, err := o.post(ctx, req, true)
	if err != nil {
		return nil, err
	}
	return newStream(resp.Body), nil
}

func (o *OpenAI) post(ctx context.Context, req Request, stream bool) (*http.Response, error) {
	body := openAIRequest{
		Model:       o.model,
		Messages:    toWireMessages(req.Messages),
		Tools:       req.Tools,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      stream,
	}
	if req.Model != "" {
		body.Model = req.Model
	}
	if stream {
		body.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("编码模型请求: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	if o.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求模型服务: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%w: HTTP %d: %s", ErrUpstream, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// --- OpenAI wire types ---

type openAIRequest struct {
	Model         string         `json:"model"`
	Messages      []Message      `json:"messages"`
	Tools         []Tool         `json:"tools,omitempty"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIResponse struct {
	Choices []openAIChoice  `json:"choices"`
	Usage   *Usage          `json:"usage"`
	Error   *openAIAPIError `json:"error"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	Delta        openAIMessage `json:"delta"`
	FinishReason string        `json:"finish_reason"`
}

type openAIMessage struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls"`
	ToolCallID string     `json:"tool_call_id"`
	Name       string     `json:"name"`
}

type openAIAPIError struct {
	Message string `json:"message"`
}

// toWireMessages 补齐历史消息中可能缺失的默认值。
func toWireMessages(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		cp := m
		for i := range cp.ToolCalls {
			if cp.ToolCalls[i].Type == "" {
				cp.ToolCalls[i].Type = "function"
			}
		}
		out = append(out, cp)
	}
	return out
}
