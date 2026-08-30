// Package prometheus 提供 Prometheus 指标查询工具。
package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

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
		return nil, fmt.Errorf("prometheus request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus status %d: %s", resp.StatusCode, string(body))
	}

	var pr prometheusResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s %s", pr.ErrorType, pr.Error)
	}
	return pr.Data, nil
}

// EinoTool 返回 Eino 工具表示。
func (t *Tool) EinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"prometheus_query",
		"执行 PromQL 即时查询，返回监控指标时间序列。用于验证错误率、延迟、CPU、内存、队列深度等监控假设。",
		t.query,
	)
}
