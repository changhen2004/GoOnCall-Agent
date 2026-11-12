package llm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// stream 解析 OpenAI 兼容 SSE，并在结束时组装完整响应。
//
// 只有收到 [DONE] 才视为正常结束；工具调用必须同时具备 id、名称和有效 JSON 参数，
// 否则 Response 返回 ErrIncomplete，调用方不得执行这些工具。
type stream struct {
	body     io.ReadCloser
	scanner  *bufio.Scanner
	content  strings.Builder
	calls    map[int]*assembledCall
	usage    *Usage
	finish   string
	done     bool
	response *Response
}

type assembledCall struct {
	id        string
	name      string
	arguments strings.Builder
}

func newStream(body io.ReadCloser) *stream {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	return &stream{
		body:    body,
		scanner: scanner,
		calls:   make(map[int]*assembledCall),
	}
}

// Recv 返回下一个增量；io.EOF 表示已收到 [DONE]。
// 未收到 [DONE] 就断流会返回 ErrIncomplete，而不是把残缺响应当作成功。
func (s *stream) Recv() (StreamChunk, error) {
	for {
		if s.done {
			return StreamChunk{}, io.EOF
		}
		if !s.scanner.Scan() {
			if err := s.scanner.Err(); err != nil {
				return StreamChunk{}, fmt.Errorf("读取 SSE 流: %w", err)
			}
			return StreamChunk{}, fmt.Errorf("%w: SSE 流在 [DONE] 前结束", ErrIncomplete)
		}

		line := strings.TrimSpace(s.scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			s.done = true
			return StreamChunk{}, io.EOF
		}

		var event streamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return StreamChunk{}, fmt.Errorf("解析 SSE 数据: %w", err)
		}
		if event.Error != nil {
			return StreamChunk{}, fmt.Errorf("%w: %s", ErrUpstream, event.Error.Message)
		}
		if event.Usage != nil {
			s.usage = event.Usage
		}

		chunk := StreamChunk{Usage: event.Usage}
		for _, choice := range event.Choices {
			chunk.Content += choice.Delta.Content
			s.content.WriteString(choice.Delta.Content)

			for _, delta := range choice.Delta.ToolCalls {
				call := s.callAt(delta.Index)
				if delta.ID != "" {
					call.id = delta.ID
				}
				if delta.Function.Name != "" {
					call.name = delta.Function.Name
				}
				call.arguments.WriteString(delta.Function.Arguments)
				chunk.ToolCalls = append(chunk.ToolCalls, ToolCallDelta{
					Index:     delta.Index,
					ID:        delta.ID,
					Name:      delta.Function.Name,
					Arguments: delta.Function.Arguments,
				})
			}

			if choice.FinishReason != "" {
				s.finish = choice.FinishReason
				chunk.FinishReason = choice.FinishReason
			}
		}

		if chunk.Content != "" || len(chunk.ToolCalls) > 0 || chunk.FinishReason != "" || chunk.Usage != nil {
			return chunk, nil
		}
	}
}

// Response 在 Recv 返回 io.EOF 后返回组装结果。
func (s *stream) Response() (*Response, error) {
	if s.response != nil {
		return s.response, nil
	}
	if !s.done {
		return nil, fmt.Errorf("%w: 流尚未正常结束", ErrIncomplete)
	}

	indexes := make([]int, 0, len(s.calls))
	for idx := range s.calls {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	calls := make([]ToolCall, 0, len(indexes))
	for _, idx := range indexes {
		call := s.calls[idx]
		if call.id == "" || call.name == "" {
			return nil, fmt.Errorf("%w: 工具调用缺少 id 或名称", ErrIncomplete)
		}
		args := strings.TrimSpace(call.arguments.String())
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) || !strings.HasPrefix(args, "{") {
			return nil, fmt.Errorf("%w: 工具 %s 的参数不是有效 JSON 对象", ErrIncomplete, call.name)
		}
		calls = append(calls, ToolCall{
			ID:   call.id,
			Type: "function",
			Function: FunctionCall{
				Name:      call.name,
				Arguments: args,
			},
		})
	}

	s.response = &Response{
		Content:      s.content.String(),
		ToolCalls:    calls,
		FinishReason: s.finish,
		Usage:        s.usage,
	}
	return s.response, nil
}

// Close 关闭响应体。
func (s *stream) Close() error { return s.body.Close() }

func (s *stream) callAt(index int) *assembledCall {
	call, ok := s.calls[index]
	if !ok {
		call = &assembledCall{}
		s.calls[index] = call
	}
	return call
}

// --- SSE wire types ---

type streamEvent struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage          `json:"usage"`
	Error *openAIAPIError `json:"error"`
}
