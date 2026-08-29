// Package rabbitmq 提供 RabbitMQ 队列检查工具。
package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

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
		return nil, fmt.Errorf("rabbitmq request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rabbitmq status %d: %s", resp.StatusCode, string(body))
	}

	var queue map[string]any
	if err := json.Unmarshal(body, &queue); err != nil {
		return nil, fmt.Errorf("decode rabbitmq response: %w", err)
	}
	return queue, nil
}

// EinoTool 返回 Eino 工具表示。
func (t *Tool) EinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"rabbitmq.inspect",
		"检查 RabbitMQ 队列状态，返回消息积压数、消费者数量、消息速率等。用于验证消费者异常、消息积压等假设。",
		t.inspect,
	)
}
