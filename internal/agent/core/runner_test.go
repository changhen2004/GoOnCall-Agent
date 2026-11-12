package core

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/tools"
)

type fakeStream struct {
	response *llm.Response
	sent     bool
}

func (s *fakeStream) Recv() (llm.StreamChunk, error) {
	if s.sent {
		return llm.StreamChunk{}, io.EOF
	}
	s.sent = true
	return llm.StreamChunk{Content: s.response.Content, FinishReason: s.response.FinishReason}, nil
}

func (s *fakeStream) Response() (*llm.Response, error) { return s.response, nil }
func (s *fakeStream) Close() error                     { return nil }

type fakeProvider struct {
	responses []*llm.Response
	index     int
	seen      []llm.Request
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, nil
}

func (p *fakeProvider) ChatStream(_ context.Context, req llm.Request) (llm.Stream, error) {
	p.seen = append(p.seen, req)
	response := p.responses[p.index]
	p.index++
	return &fakeStream{response: response}, nil
}

type fakeHooks struct {
	messages []llm.Message
	began    int
	beginErr error
}

func (h *fakeHooks) SaveMessages(_ context.Context, messages []llm.Message) error {
	h.messages = append(h.messages, messages...)
	return nil
}

func (h *fakeHooks) SaveDraft(context.Context, string) error { return nil }

func (h *fakeHooks) BeginTool(context.Context, llm.ToolCall, tools.Tool, json.RawMessage) error {
	h.began++
	return h.beginErr
}

func (h *fakeHooks) EndTool(_ context.Context, call llm.ToolCall, _ ToolStatus, content string) error {
	// 真实会话层会在同一事务中保存工具消息；测试里模拟这一行为。
	h.messages = append(h.messages, llm.Message{
		Role:       llm.RoleTool,
		Content:    content,
		ToolCallID: call.ID,
		Name:       call.Function.Name,
	})
	return nil
}

func toolCallResponse(name, args string) *llm.Response {
	return &llm.Response{
		FinishReason: "tool_calls",
		ToolCalls: []llm.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: llm.FunctionCall{Name: name, Arguments: args},
		}},
	}
}

func newEchoRegistry(t *testing.T, requiresApproval bool, executed *bool) *tools.Registry {
	t.Helper()
	registry := tools.NewRegistry()
	err := registry.Register(tools.Tool{
		Name:             "echo",
		Description:      "回显参数",
		RequiresApproval: requiresApproval,
		Run: func(_ context.Context, args json.RawMessage) (string, error) {
			*executed = true
			return "ok:" + string(args), nil
		},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return registry
}

func TestRunnerExecutesToolAndContinues(t *testing.T) {
	executed := false
	provider := &fakeProvider{responses: []*llm.Response{
		toolCallResponse("echo", `{"text":"hi"}`),
		{Content: "完成", FinishReason: "stop"},
	}}
	hooks := &fakeHooks{}
	runner := New(provider, newEchoRegistry(t, false, &executed), hooks, nil)

	result, err := runner.Run(context.Background(), RunConfig{
		User:            "开始",
		MaxRounds:       3,
		ContextWindow:   8192,
		MaxOutputTokens: 256,
		Budget:          time.Minute,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != StatusCompleted || result.Content != "完成" {
		t.Fatalf("result = %+v", result)
	}
	if !executed {
		t.Fatal("工具未执行")
	}
	if len(hooks.messages) != 3 {
		t.Fatalf("保存消息数 = %d", len(hooks.messages))
	}
	if len(provider.seen) != 2 {
		t.Fatalf("模型调用次数 = %d", len(provider.seen))
	}

	second := provider.seen[1].Messages
	last := second[len(second)-1]
	if last.Role != llm.RoleTool || last.ToolCallID != "call_1" {
		t.Fatalf("工具结果未正确回填: %+v", last)
	}
}

func TestRunnerBackfillsRejectedTool(t *testing.T) {
	executed := false
	provider := &fakeProvider{responses: []*llm.Response{
		toolCallResponse("echo", `{"text":"hi"}`),
		{Content: "好的", FinishReason: "stop"},
	}}
	hooks := &fakeHooks{beginErr: ErrToolRejected}
	runner := New(provider, newEchoRegistry(t, true, &executed), hooks, nil)

	result, err := runner.Run(context.Background(), RunConfig{
		User:            "开始",
		MaxRounds:       3,
		ContextWindow:   8192,
		MaxOutputTokens: 256,
		Budget:          time.Minute,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("status = %s", result.Status)
	}
	if executed {
		t.Fatal("被拒绝的工具不应执行")
	}
	if len(hooks.messages) != 3 {
		t.Fatalf("保存消息数 = %d", len(hooks.messages))
	}
	if !strings.Contains(hooks.messages[1].Content, "拒绝") {
		t.Fatalf("拒绝结果未回填: %q", hooks.messages[1].Content)
	}
}
