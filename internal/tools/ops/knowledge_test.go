package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	kmodel "gooncall-agent/internal/knowledge/model"
	"gooncall-agent/internal/knowledge/retriever"
)

type stubRetriever struct {
	results []retriever.Result
	err     error
}

func (s stubRetriever) Retrieve(context.Context, string, int) ([]retriever.Result, error) {
	return s.results, s.err
}

func TestKnowledgeSearchReturnsSourcesAndTruncates(t *testing.T) {
	content := strings.Repeat("长内容", 100)
	tool := NewKnowledgeTool(KnowledgeOptions{
		Provider: func(context.Context) (retriever.Retriever, error) {
			return stubRetriever{results: []retriever.Result{{
				Chunk: &kmodel.Chunk{
					Source: "rabbitmq.md", DocID: "runbooks/rabbitmq.md", Title: "队列积压",
					Content: content, SourceURL: "https://example.test/rabbitmq", LastVerifiedAt: "2026-01-01",
				},
				Score: 0.9,
			}}}, nil
		},
		TopK: 5, MaxContentChars: 10, Timeout: time.Second,
	})

	out, err := tool.Run(context.Background(), json.RawMessage(`{"query":"队列积压"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var response struct {
		Results []struct {
			Source           string `json:"source"`
			Content          string `json:"content"`
			ContentTruncated bool   `json:"content_truncated"`
		} `json:"results"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Source != "rabbitmq.md" ||
		!response.Results[0].ContentTruncated || !response.Truncated {
		t.Fatalf("response = %+v", response)
	}
	if len([]rune(response.Results[0].Content)) > 11 {
		t.Fatalf("content = %q", response.Results[0].Content)
	}
}

func TestKnowledgeSearchNoResultAndFailure(t *testing.T) {
	noResult := NewKnowledgeTool(KnowledgeOptions{
		Provider: func(context.Context) (retriever.Retriever, error) { return stubRetriever{}, nil },
	})
	out, err := noResult.Run(context.Background(), json.RawMessage(`{"query":"未知"}`))
	if err != nil || !strings.Contains(out, `"results":[]`) {
		t.Fatalf("无结果 = %s, %v", out, err)
	}

	failing := NewKnowledgeTool(KnowledgeOptions{
		Provider: func(context.Context) (retriever.Retriever, error) {
			return nil, errors.New("qdrant down")
		},
	})
	if _, err := failing.Run(context.Background(), json.RawMessage(`{"query":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "不可用") {
		t.Fatalf("失败错误 = %v", err)
	}
}
