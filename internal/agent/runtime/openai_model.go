package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// OpenAIChatModel 是 OpenAI 兼容的 ChatModel 实现（Eino model.ToolCallingChatModel）。
//
// 通过标准 chat/completions 协议对接 OpenAI、DeepSeek、Ollama、LM Studio 等兼容端点。
type OpenAIChatModel struct {
	client  *http.Client
	baseURL string
	apiKey  string
	model   string
	tools   []*schema.ToolInfo
}

// NewOpenAIChatModel 创建 OpenAI 兼容模型。
func NewOpenAIChatModel(baseURL, apiKey, modelName string) *OpenAIChatModel {
	return &OpenAIChatModel{
		client:  &http.Client{},
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   modelName,
	}
}

// WithTools 返回绑定了工具的新实例（不可变，线程安全）。
func (m *OpenAIChatModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	cp := *m
	cp.tools = tools
	return &cp, nil
}

// Generate 发起一次非流式对话补全。
func (m *OpenAIChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	common := model.GetCommonOptions(&model.Options{}, opts...)

	modelName := m.model
	if common.Model != nil && *common.Model != "" {
		modelName = *common.Model
	}

	req := openAIRequest{
		Model:    modelName,
		Messages: toOpenAIMessages(input),
		Tools:    toOpenAITools(m.tools),
	}
	if common.Temperature != nil {
		req.Temperature = common.Temperature
	}
	if common.MaxTokens != nil {
		req.MaxTokens = common.MaxTokens
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai status %d: %s", resp.StatusCode, string(respBody))
	}

	var oaResp openAIResponse
	if err := json.Unmarshal(respBody, &oaResp); err != nil {
		return nil, fmt.Errorf("decode openai response: %w", err)
	}
	if oaResp.Error != nil {
		return nil, fmt.Errorf("openai error: %s", oaResp.Error.Message)
	}
	if len(oaResp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}
	return toSchemaMessage(oaResp.Choices[0].Message), nil
}

// Stream 以非流式方式实现 Stream 接口（真实 SSE 流式在 Phase 4 接入）。
func (m *OpenAIChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// --- OpenAI wire types ---

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature *float32        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Tools       []openAITool    `json:"tools,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message      openAIMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func toOpenAIMessages(msgs []*schema.Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(msgs))
	for _, m := range msgs {
		om := openAIMessage{
			Role:    string(m.Role),
			Content: m.Content,
		}
		if m.Role == schema.Tool {
			om.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: openAIFunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
		out = append(out, om)
	}
	return out
}

func toOpenAITools(infos []*schema.ToolInfo) []openAITool {
	out := make([]openAITool, 0, len(infos))
	for _, ti := range infos {
		var paramsJSON json.RawMessage
		if ti != nil && ti.ParamsOneOf != nil {
			if params, err := ti.ToJSONSchema(); err == nil && params != nil {
				paramsJSON, _ = json.Marshal(params)
			}
		}
		out = append(out, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        ti.Name,
				Description: ti.Desc,
				Parameters:  paramsJSON,
			},
		})
	}
	return out
}

func toSchemaMessage(om openAIMessage) *schema.Message {
	msg := &schema.Message{
		Role:    schema.RoleType(om.Role),
		Content: om.Content,
	}
	for _, tc := range om.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return msg
}
