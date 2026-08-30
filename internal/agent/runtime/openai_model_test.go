package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

type echoInput struct {
	X string `json:"x" jsonschema:"required,description=x"`
}

func makeToolInfo(t *testing.T) *schema.ToolInfo {
	t.Helper()
	tl, err := toolutils.InferTool("test.echo", "echo tool", func(ctx context.Context, in echoInput) (string, error) {
		return in.X, nil
	})
	if err != nil {
		t.Fatalf("InferTool: %v", err)
	}
	info, err := tl.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	return info
}

func TestGenerate_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"根因是消费者连接异常"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	m := NewOpenAIChatModel(server.URL, "sk-test", "gpt-4o")
	msg, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "诊断故障"}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if msg.Content != "根因是消费者连接异常" {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("unexpected tool calls: %+v", msg.ToolCalls)
	}
}

func TestGenerate_ToolCallResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"prometheus_query","arguments":"{\"query\":\"up\"}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()

	m := NewOpenAIChatModel(server.URL, "sk-test", "gpt-4o")
	msg, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "查指标"}})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Function.Name != "prometheus_query" || tc.Function.Arguments != `{"query":"up"}` {
		t.Fatalf("tool call = %+v", tc)
	}
}

func TestGenerate_SendsMessagesAndTools(t *testing.T) {
	var captured openAIRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	m := NewOpenAIChatModel(server.URL, "sk-test", "gpt-4o")
	withTools, err := m.WithTools([]*schema.ToolInfo{makeToolInfo(t)})
	if err != nil {
		t.Fatalf("WithTools() error = %v", err)
	}

	_, err = withTools.Generate(context.Background(), []*schema.Message{
		{Role: schema.System, Content: "你是诊断 agent"},
		{Role: schema.User, Content: "故障"},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if captured.Model != "gpt-4o" {
		t.Errorf("model = %q", captured.Model)
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(captured.Messages))
	}
	if len(captured.Tools) != 1 || captured.Tools[0].Function.Name != "test.echo" {
		t.Fatalf("tools = %+v", captured.Tools)
	}
}

func TestWithTools_Immutability(t *testing.T) {
	m := NewOpenAIChatModel("http://x", "k", "m")
	withTools, err := m.WithTools([]*schema.ToolInfo{makeToolInfo(t)})
	if err != nil {
		t.Fatalf("WithTools() error = %v", err)
	}
	if len(m.tools) != 0 {
		t.Fatalf("original model tools mutated: %d", len(m.tools))
	}
	if len(withTools.(*OpenAIChatModel).tools) != 1 {
		t.Fatalf("new model tools = %d, want 1", len(withTools.(*OpenAIChatModel).tools))
	}
}

func TestGenerate_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	m := NewOpenAIChatModel(server.URL, "bad", "m")
	if _, err := m.Generate(context.Background(), []*schema.Message{{Role: schema.User, Content: "x"}}); err == nil {
		t.Fatal("expected error for 401")
	}
}
