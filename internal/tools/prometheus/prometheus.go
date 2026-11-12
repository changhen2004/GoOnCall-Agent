// Package prometheus 提供 Prometheus 指标查询工具。
package prometheus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var (
	// ErrUnavailable 表示 Prometheus 不可达或返回 5xx。
	ErrUnavailable = errors.New("prometheus unavailable")
	// ErrInvalidResponse 表示响应格式无法解析。
	ErrInvalidResponse = errors.New("prometheus invalid response")
	// ErrQuery 表示 PromQL 查询失败（4xx 或 status=error）。
	ErrQuery = errors.New("prometheus query failed")
)

// QueryError 携带安全的查询错误摘要，不包含响应正文或配置。
type QueryError struct {
	Detail string
}

func (e *QueryError) Error() string { return ErrQuery.Error() + ": " + e.Detail }
func (e *QueryError) Is(target error) bool {
	return target == ErrQuery
}

// Tool 查询 Prometheus 指标（Read Only，风险 LOW）。
type Tool struct {
	client  *http.Client
	baseURL string
}

// New 创建 Prometheus 工具。
func New(baseURL string) *Tool {
	return &Tool{
		client:  &http.Client{},
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// QueryInput 是 PromQL 查询入参。
type QueryInput struct {
	Query string `json:"query" jsonschema:"required,description=PromQL 查询表达式"`
}

type prometheusResponse struct {
	Status    string         `json:"status"`
	Data      map[string]any `json:"data"`
	ErrorType string         `json:"errorType"`
	Error     string         `json:"error"`
}

func (t *Tool) query(ctx context.Context, in QueryInput) (map[string]any, error) {
	u := t.baseURL + "/api/v1/query"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("query", in.Query)
	req.URL.RawQuery = q.Encode()

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w", ErrUnavailable)
		}
		return nil, &QueryError{Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	var pr prometheusResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("%w", ErrInvalidResponse)
	}
	if pr.Status != "success" {
		return nil, &QueryError{Detail: strings.TrimSpace(pr.ErrorType + " " + pr.Error)}
	}
	return pr.Data, nil
}

// Run 解析明确输入结构，返回可供 Agent 使用的 JSON 结果。
func (t *Tool) Run(ctx context.Context, arguments string) (string, error) {
	var input QueryInput
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return "", err
	}
	result, err := t.query(ctx, input)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}
