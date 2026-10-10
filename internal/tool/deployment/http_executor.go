package deployment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"gooncall-agent/internal/execution/approval"
)

// HTTPExecutorConfig 是固定 HTTP 处置适配器配置。
//
// URL 由运维配置固定注入，LLM 不能提供 URL、命令或凭证；target 白名单在工具层校验。
type HTTPExecutorConfig struct {
	URL     string
	Token   string
	Timeout time.Duration
}

// HTTPExecutor 调用固定 endpoint 执行 restart_worker。
//
// 幂等键为 approval_id（同时作为 Idempotency-Key 请求头）。
type HTTPExecutor struct {
	client *http.Client
	url    string
	token  string
}

// NewHTTPExecutor 创建固定 URL 的 HTTP 处置适配器。
func NewHTTPExecutor(cfg HTTPExecutorConfig) (*HTTPExecutor, error) {
	if cfg.URL == "" {
		return nil, errors.New("execution.restart_url is required for the HTTP executor")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &HTTPExecutor{
		client: &http.Client{Timeout: cfg.Timeout},
		url:    cfg.URL,
		token:  cfg.Token,
	}, nil
}

// Execute 发送 {action, approval_id, arguments} JSON 请求并返回归一化结果。
//
// 分类：
//   - 2xx：成功，解析 operation_id（缺失则为空）；
//   - 超时/取消：包装 approval.ErrResultUnknown（无法确认是否已执行，禁止自动重试）；
//   - 4xx/5xx：明确失败，返回普通错误（Approval 落 FAILED）。
//
// 错误文本只带状态码，不带响应体与 Authorization header，避免 Token 泄露。
func (e *HTTPExecutor) Execute(ctx context.Context, action, arguments string) (approval.ExecutionResult, error) {
	approvalID := approval.ApprovalIDFrom(ctx)
	if approvalID == "" {
		// 没有幂等键就无法保证"同一审批只执行一次"，宁可不调用外部系统。
		return approval.ExecutionResult{}, errors.New("missing approval_id: refusing to call the deployment endpoint without an idempotency key")
	}

	args := json.RawMessage(arguments)
	if !json.Valid(args) {
		args = json.RawMessage("{}")
	}
	payload, err := json.Marshal(map[string]any{
		"action":      action,
		"approval_id": approvalID,
		"arguments":   args,
	})
	if err != nil {
		return approval.ExecutionResult{}, fmt.Errorf("encode deployment request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(payload))
	if err != nil {
		return approval.ExecutionResult{}, fmt.Errorf("build deployment request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", approvalID)
	if e.token != "" {
		req.Header.Set("Authorization", "Bearer "+e.token)
	}

	resp, err := e.client.Do(req)
	if err != nil {
		if isTimeoutErr(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			// 超时/取消：请求可能已经到达并被执行 -> 结果未知，禁止当作明确失败后自动重试。
			return approval.ExecutionResult{}, fmt.Errorf("%w: deployment endpoint call: %v", approval.ErrResultUnknown, err)
		}
		return approval.ExecutionResult{}, fmt.Errorf("call deployment endpoint: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return approval.ExecutionResult{}, fmt.Errorf("deployment endpoint returned status %d", resp.StatusCode)
	}

	var out struct {
		OperationID string `json:"operation_id"`
	}
	_ = json.Unmarshal(body, &out)

	result, err := json.Marshal(map[string]any{
		"status":       "completed",
		"operation_id": out.OperationID,
	})
	if err != nil {
		return approval.ExecutionResult{}, fmt.Errorf("encode deployment result: %w", err)
	}
	return approval.ExecutionResult{Output: string(result), OperationID: out.OperationID}, nil
}

// maxResponseBytes 限制读取的响应体大小（只用于解析 operation_id）。
const maxResponseBytes = 4096

// isTimeoutErr 判断是否为网络超时。
func isTimeoutErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
