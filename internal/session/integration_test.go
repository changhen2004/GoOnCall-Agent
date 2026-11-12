package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/session"
	"gooncall-agent/internal/store"
	"gooncall-agent/internal/tools"
)

// fakeProvider 按顺序返回预设响应，用于验证核心与会话存储的集成。
type fakeProvider struct {
	responses []*llm.Response
	index     int
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("测试中不使用普通调用")
}

func (p *fakeProvider) ChatStream(context.Context, llm.Request) (llm.Stream, error) {
	response := p.responses[p.index]
	if p.index < len(p.responses)-1 {
		p.index++
	}
	return &fakeStream{response: response}, nil
}

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

func TestApprovalFlowEndToEnd(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	executed := make(chan struct{}, 1)
	registry := tools.NewRegistry()
	err = registry.Register(tools.Tool{
		Name:             "write_demo",
		Description:      "测试写操作",
		RequiresApproval: true,
		Run: func(context.Context, json.RawMessage) (string, error) {
			executed <- struct{}{}
			return "written", nil
		},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	provider := &fakeProvider{responses: []*llm.Response{
		{
			FinishReason: "tool_calls",
			ToolCalls: []llm.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: llm.FunctionCall{Name: "write_demo", Arguments: `{"path":"a.txt"}`},
			}},
		},
		{Content: "完成", FinishReason: "stop"},
	}}

	svc, err := session.New(db, session.Options{
		Models: map[string]session.Model{
			"fake": {Provider: provider, ContextWindow: 8192, MaxOutputTokens: 512},
		},
		DefaultModel: "fake",
		MaxRounds:    4,
		RunBudget:    time.Minute,
		ApprovalTTL:  time.Minute,
		Registry:     registry,
		ProcessID:    "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	conversation, err := svc.CreateConversation(ctx, "alice")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	run, err := svc.Submit(ctx, "alice", conversation.ID, "请写入文件", "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitFor(t, func() bool {
		detail, err := svc.RunDetail(ctx, "alice", run.ID)
		return err == nil && detail.Run.Status == session.RunWaitingApproval && len(detail.Approvals) == 1
	})

	// 等待审批期间，同一会话的新消息直接拒绝。
	if _, err := svc.Submit(ctx, "alice", conversation.ID, "再来一条", ""); !errors.Is(err, session.ErrRunActive) {
		t.Fatalf("并发提交错误 = %v", err)
	}

	detail, err := svc.RunDetail(ctx, "alice", run.ID)
	if err != nil {
		t.Fatalf("RunDetail: %v", err)
	}
	approval := detail.Approvals[0]
	if err := svc.Decide(ctx, "alice", approval.ID, true); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if err := svc.Decide(ctx, "alice", approval.ID, true); !errors.Is(err, session.ErrConflict) {
		t.Fatalf("重复批准错误 = %v", err)
	}

	waitFor(t, func() bool {
		detail, err := svc.RunDetail(ctx, "alice", run.ID)
		return err == nil && detail.Run.Status == session.RunCompleted
	})
	select {
	case <-executed:
	default:
		t.Fatal("审批通过后工具未执行")
	}

	_, messages, _, err := svc.Conversation(ctx, "alice", conversation.ID)
	if err != nil {
		t.Fatalf("Conversation: %v", err)
	}
	var hasToolResult, hasFinal bool
	for _, message := range messages {
		if message.Role == llm.RoleTool && message.Content == "written" {
			hasToolResult = true
		}
		if message.Role == llm.RoleAssistant && message.Content == "完成" {
			hasFinal = true
		}
	}
	if !hasToolResult || !hasFinal {
		t.Fatalf("消息记录不完整: %+v", messages)
	}

	detail, err = svc.RunDetail(ctx, "alice", run.ID)
	if err != nil {
		t.Fatalf("RunDetail: %v", err)
	}
	if len(detail.Tools) != 1 || detail.Tools[0].Status != session.ToolSucceeded {
		t.Fatalf("工具执行记录 = %+v", detail.Tools)
	}
}

// waitFor 轮询后台运行状态，避免依赖固定 sleep 时长。
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
