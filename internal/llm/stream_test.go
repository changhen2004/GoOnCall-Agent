package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStreamAssemblesFragmentedToolCall 验证分片工具参数能被完整组装。
func TestStreamAssemblesFragmentedToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		lines := []string{
			`data: {"choices":[{"delta":{"content":"正在读取"},"finish_reason":null}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"demo_read_file","arguments":"{\"pa"}}]}}]}`,
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`data: [DONE]`,
		}
		for _, line := range lines {
			fmt.Fprintln(w, line)
			fmt.Fprintln(w)
		}
	}))
	defer server.Close()

	provider := NewOpenAI("test", server.URL, "", "test-model")
	stream, err := provider.ChatStream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()

	var content string
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		content += chunk.Content
	}

	response, err := stream.Response()
	if err != nil {
		t.Fatalf("Response: %v", err)
	}
	if content != "正在读取" || response.Content != "正在读取" {
		t.Fatalf("content = %q / %q", content, response.Content)
	}
	if len(response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d", len(response.ToolCalls))
	}
	call := response.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "demo_read_file" {
		t.Fatalf("tool call = %+v", call)
	}
	if call.Function.Arguments != `{"path":"a.txt"}` {
		t.Fatalf("arguments = %s", call.Function.Arguments)
	}
}

func TestStreamRejectsTruncatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"半截"}}]}`)
		fmt.Fprintln(w)
	}))
	defer server.Close()

	provider := NewOpenAI("test", server.URL, "", "test-model")
	stream, err := provider.ChatStream(context.Background(), Request{})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	defer stream.Close()

	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			t.Fatal("流未收到 [DONE] 却返回了 EOF")
		}
		if err != nil {
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("错误类型 = %v", err)
			}
			return
		}
	}
}
