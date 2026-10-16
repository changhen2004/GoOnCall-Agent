// Package reranker 提供候选片段重排能力。
package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// TEI 调用 Hugging Face Text Embeddings Inference 的 /rerank 接口。
type TEI struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewTEI 创建 TEI 重排客户端。模型由 TEI 服务启动参数决定，不随请求传入。
func NewTEI(baseURL, apiKey string) *TEI {
	return &TEI{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  apiKey,
		client:  http.DefaultClient,
	}
}

// Rerank 返回与输入 documents 顺序对齐的相关性分数。
func (t *TEI) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(struct {
		Query      string   `json:"query"`
		Texts      []string `json:"texts"`
		Truncate   bool     `json:"truncate"`
		RawScores  bool     `json:"raw_scores"`
		ReturnText bool     `json:"return_text"`
	}{
		Query:      query,
		Texts:      documents,
		Truncate:   true,
		RawScores:  false,
		ReturnText: false,
	})
	if err != nil {
		return nil, fmt.Errorf("encode rerank request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/rerank", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call reranker: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("reranker returned status %d", resp.StatusCode)
	}

	var ranks []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ranks); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}
	if len(ranks) != len(documents) {
		return nil, fmt.Errorf("reranker returned %d scores for %d documents", len(ranks), len(documents))
	}

	scores := make([]float64, len(documents))
	seen := make([]bool, len(documents))
	for _, rank := range ranks {
		if rank.Index < 0 || rank.Index >= len(documents) || seen[rank.Index] {
			return nil, fmt.Errorf("reranker returned invalid document index %d", rank.Index)
		}
		seen[rank.Index] = true
		scores[rank.Index] = rank.Score
	}
	return scores, nil
}
