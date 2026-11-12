package core

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"gooncall-agent/internal/llm"
)

// contextSafetyMargin 为协议开销预留的估算余量。
const contextSafetyMargin = 512

// Turn 是一轮完整历史：用户输入、完整的模型回复、工具调用及其结果。
// 上下文裁剪以 Turn 为最小单位，不会拆开工具调用与对应结果。
type Turn struct {
	Messages []llm.Message
}

// EstimateTokens 粗略估算 token 数。
//
// 这是保守近似，不是任何厂商的精确计数：ASCII 约 4 字符 1 token，
// 非 ASCII 字符按 1 字符 1 token 计算。上游仍可能返回超限错误。
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii, other := 0, 0
	for _, r := range s {
		if r < utf8.RuneSelf {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other
}

// buildInput 组装本次模型输入：系统提示、按预算选择的最近完整历史、本轮消息。
// 历史按 Turn 整体取舍，遇到第一个放不下的轮次即停止，保证工具调用与结果成对。
func buildInput(system string, toolDefs []llm.Tool, history []Turn, current []llm.Message, contextWindow, maxOutput int) ([]llm.Message, error) {
	budget := contextWindow - maxOutput - contextSafetyMargin
	if budget <= 0 {
		return nil, fmt.Errorf("上下文窗口 %d 不足以为输出预留空间", contextWindow)
	}

	systemMsg := llm.Message{Role: llm.RoleSystem, Content: system}
	used := messageTokens(systemMsg) + toolsTokens(toolDefs)
	for _, msg := range current {
		used += messageTokens(msg)
	}
	if used > budget {
		return nil, fmt.Errorf("本轮必要输入约 %d tokens，超出可用预算 %d", used, budget)
	}

	// 从最近向前选择连续的完整轮次；遇到放不下的轮次立即停止，保持连续性。
	start := len(history)
	for i := len(history) - 1; i >= 0; i-- {
		cost := turnTokens(history[i])
		if used+cost > budget {
			break
		}
		used += cost
		start = i
	}

	out := make([]llm.Message, 0, len(current)+len(history)-start+8)
	out = append(out, systemMsg)
	for _, turn := range history[start:] {
		out = append(out, turn.Messages...)
	}
	out = append(out, current...)
	return out, nil
}

func turnTokens(turn Turn) int {
	total := 0
	for _, msg := range turn.Messages {
		total += messageTokens(msg)
	}
	return total
}

func messageTokens(msg llm.Message) int {
	total := 4 + EstimateTokens(msg.Content) + EstimateTokens(msg.Name) + EstimateTokens(msg.ToolCallID)
	for _, call := range msg.ToolCalls {
		total += 8 + EstimateTokens(call.ID) + EstimateTokens(call.Function.Name) + EstimateTokens(call.Function.Arguments)
	}
	return total
}

func toolsTokens(defs []llm.Tool) int {
	total := 0
	for _, def := range defs {
		data, err := json.Marshal(def)
		if err != nil {
			continue
		}
		total += EstimateTokens(string(data)) + 4
	}
	return total
}
