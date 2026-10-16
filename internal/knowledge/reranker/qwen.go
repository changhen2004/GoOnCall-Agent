package reranker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Qwen 调用 DashScope 文本重排接口。
type Qwen struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

// NewQwen 创建 Qwen 重排客户端。
func NewQwen(baseURL, apiKey, model string) *Qwen {
	return &Qwen{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:  apiKey,
		model:   model,
		client:  http.DefaultClient,
	}
}

// Rerank 返回与输入 documents 顺序对齐的相关性分数。
func (q *Qwen) Rerank(ctx context.Context, query string, documents []string) ([]float64, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	var request struct {
		Model string `json:"model"`
		Input struct {
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
		} `json:"input"`
		Parameters struct {
			ReturnDocuments bool `json:"return_documents"`
		} `json:"parameters"`
	}
	request.Model = q.model
	request.Input.Query = query
	request.Input.Documents = documents
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode rerank request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.baseURL+"/services/rerank/text-rerank/text-rerank", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create rerank request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+q.apiKey)

	resp, err := q.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call reranker: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("reranker returned status %d", resp.StatusCode)
	}

	var response struct {
		Output struct {
			Results []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
			} `json:"results"`
		} `json:"output"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode rerank response: %w", err)
	}
	if len(response.Output.Results) != len(documents) {
		return nil, fmt.Errorf("reranker returned %d scores for %d documents", len(response.Output.Results), len(documents))
	}

	scores := make([]float64, len(documents))
	seen := make([]bool, len(documents))
	for _, result := range response.Output.Results {
		if result.Index < 0 || result.Index >= len(documents) || seen[result.Index] {
			return nil, fmt.Errorf("reranker returned invalid document index %d", result.Index)
		}
		seen[result.Index] = true
		scores[result.Index] = result.RelevanceScore
	}
	return scores, nil
}
