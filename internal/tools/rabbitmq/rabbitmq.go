// Package rabbitmq 提供 RabbitMQ 队列检查工具。
package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	// ErrUnavailable 表示 Management API 不可达或返回 5xx。
	ErrUnavailable = errors.New("rabbitmq management unavailable")
	// ErrInvalidResponse 表示响应格式无法解析。
	ErrInvalidResponse = errors.New("rabbitmq invalid response")
	// ErrNotFound 表示队列不存在。
	ErrNotFound = errors.New("rabbitmq queue not found")
	// ErrQuery 表示 Management API 返回其他错误。
	ErrQuery = errors.New("rabbitmq query failed")
)

// QueryError 携带安全的错误摘要。
type QueryError struct {
	Detail string
}

func (e *QueryError) Error() string { return ErrQuery.Error() + ": " + e.Detail }
func (e *QueryError) Is(target error) bool {
	return target == ErrQuery
}

// Tool 检查 RabbitMQ 队列状态（Read Only，风险 LOW）。
type Tool struct {
	client   *http.Client
	baseURL  string
	username string
	password string
}

// New 创建 RabbitMQ 工具，baseURL 为 Management API 地址（如 http://rabbitmq:15672）。
func New(baseURL, username, password string) *Tool {
	return &Tool{
		client:   &http.Client{},
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
	}
}

// InspectInput 是队列检查入参。
type InspectInput struct {
	Queue string `json:"queue" jsonschema:"required,description=队列名称"`
	VHost string `json:"vhost,omitempty" jsonschema:"description=虚拟主机，默认 /"`
}

func (t *Tool) inspect(ctx context.Context, in InspectInput) (map[string]any, error) {
	vhost := in.VHost
	if vhost == "" {
		vhost = "%2F"
	} else {
		vhost = url.PathEscape(vhost)
	}
	u := t.baseURL + "/api/queues/" + vhost + "/" + url.PathEscape(in.Queue)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(t.username, t.password)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w", ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w", ErrUnavailable)
		}
		return nil, &QueryError{Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	var queue map[string]any
	if err := json.Unmarshal(body, &queue); err != nil {
		return nil, fmt.Errorf("%w", ErrInvalidResponse)
	}
	return queue, nil
}

// Run 解析明确输入结构，返回可供 Agent 使用的 JSON 结果。
func (t *Tool) Run(ctx context.Context, arguments string) (string, error) {
	var input InspectInput
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return "", err
	}
	result, err := t.inspect(ctx, input)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}
