package core

import (
	"strings"
	"testing"

	"gooncall-agent/internal/llm"
)

// TestBuildInputKeepsRecentWholeTurns 验证从最近向前选取完整轮次，不拆开一轮消息。
func TestBuildInputKeepsRecentWholeTurns(t *testing.T) {
	system := "系统提示"
	current := []llm.Message{{Role: llm.RoleUser, Content: "本次输入"}}
	oldTurn := Turn{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("a", 8000)},
		{Role: llm.RoleAssistant, Content: "旧回答"},
	}}
	recentTurn := Turn{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "近期问题"},
		{Role: llm.RoleAssistant, Content: "近期回答"},
	}}

	required := messageTokens(llm.Message{Role: llm.RoleSystem, Content: system}) + messageTokens(current[0])
	budget := required + turnTokens(recentTurn) + 10
	contextWindow := budget + 2048 + contextSafetyMargin

	messages, err := buildInput(system, nil, []Turn{oldTurn, recentTurn}, current, contextWindow, 2048)
	if err != nil {
		t.Fatalf("buildInput: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("消息数 = %d，期望系统 + 最近轮次 + 本次输入", len(messages))
	}
	if messages[0].Role != llm.RoleSystem || messages[1].Content != "近期问题" || messages[3].Content != "本次输入" {
		t.Fatalf("消息顺序 = %+v", messages)
	}
}

func TestBuildInputStopsWhenRequiredContentExceedsBudget(t *testing.T) {
	_, err := buildInput("系统", nil, nil, []llm.Message{
		{Role: llm.RoleUser, Content: strings.Repeat("x", 10000)},
	}, 4096, 2048)
	if err == nil {
		t.Fatal("必要内容超预算时应明确返错")
	}
}
