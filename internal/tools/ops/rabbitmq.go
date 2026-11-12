package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gooncall-agent/internal/tools"
	"gooncall-agent/internal/tools/rabbitmq"
)

// RabbitMQOptions 是只读队列查询工具配置。
type RabbitMQOptions struct {
	ManagementURL string
	Username      string
	Password      string
	Timeout       time.Duration
}

// NewRabbitMQTool 构造队列状态查询工具。
func NewRabbitMQTool(opts RabbitMQOptions) tools.Tool {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	client := rabbitmq.New(opts.ManagementURL, opts.Username, opts.Password)

	return tools.Tool{
		Name:        "rabbitmq_queue_status",
		Description: "查询 RabbitMQ 队列状态（消费者数、消息积压等）；连接地址和凭据由服务端配置。",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"queue":{"type":"string","description":"队列名称"},
				"vhost":{"type":"string","description":"虚拟主机，默认 /"}
			},
			"required":["queue"]
		}`),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var input struct {
				Queue string `json:"queue"`
				VHost string `json:"vhost"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", errors.New("参数不是有效的 JSON")
			}
			input.Queue = strings.TrimSpace(input.Queue)
			input.VHost = strings.TrimSpace(input.VHost)
			if input.Queue == "" || len([]rune(input.Queue)) > 200 {
				return "", errors.New("queue 不能为空且不能超过 200 个字符")
			}
			if len([]rune(input.VHost)) > 200 {
				return "", errors.New("vhost 不能超过 200 个字符")
			}

			callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
			raw, err := client.Run(callCtx, string(args))
			if err != nil {
				if errors.Is(err, rabbitmq.ErrNotFound) {
					return marshalJSON(map[string]any{
						"found": false,
						"queue": input.Queue,
					})
				}
				return "", rabbitMQError(err)
			}

			var queue struct {
				Name                   string `json:"name"`
				VHost                  string `json:"vhost"`
				State                  string `json:"state"`
				Consumers              int    `json:"consumers"`
				Messages               int    `json:"messages"`
				MessagesReady          int    `json:"messages_ready"`
				MessagesUnacknowledged int    `json:"messages_unacknowledged"`
			}
			if err := json.Unmarshal([]byte(raw), &queue); err != nil {
				return "", errors.New("RabbitMQ 响应格式错误")
			}

			return marshalJSON(map[string]any{
				"found":                   true,
				"name":                    queue.Name,
				"vhost":                   queue.VHost,
				"state":                   queue.State,
				"consumers":               queue.Consumers,
				"messages":                queue.Messages,
				"messages_ready":          queue.MessagesReady,
				"messages_unacknowledged": queue.MessagesUnacknowledged,
			})
		},
	}
}

func rabbitMQError(err error) error {
	var queryErr *rabbitmq.QueryError
	switch {
	case errors.As(err, &queryErr):
		return fmt.Errorf("RabbitMQ 查询失败: %s", truncateRunes(queryErr.Detail, 300))
	case errors.Is(err, rabbitmq.ErrUnavailable):
		return errors.New("RabbitMQ 服务不可达")
	case errors.Is(err, rabbitmq.ErrInvalidResponse):
		return errors.New("RabbitMQ 响应格式错误")
	default:
		return errors.New("RabbitMQ 查询失败")
	}
}

func marshalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("结果编码失败")
	}
	return string(encoded), nil
}
