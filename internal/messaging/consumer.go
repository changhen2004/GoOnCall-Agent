package messaging

import (
	"context"
	"encoding/json"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Handler 处理事件。
type Handler func(ctx context.Context, event Event) error

// Consumer 订阅事件并处理。
type Consumer struct {
	conn  *amqp.Connection
	queue string
}

// NewConsumer 创建 Consumer。
func NewConsumer(conn *amqp.Connection, queue string) *Consumer {
	return &Consumer{conn: conn, queue: queue}
}

// Subscribe 订阅指定路由键，逐条处理事件。ctx 取消时返回。
//
// 消费失败不会无限 requeue：失败消息按 x-retry-count 路由到重试队列
// （TTL 到期后重新投递），超过 MaxRetries 次后进入死信队列（DLQ）。
func (c *Consumer) Subscribe(ctx context.Context, routingKey string, handler Handler) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// 幂等声明交换机，保证 Worker 独立启动时拓扑可用。
	if err := ch.ExchangeDeclare(ExchangeAgent, "topic", true, false, false, false, nil); err != nil {
		return err
	}

	// 主队列 + 重试队列（TTL 到期后按原路由键重新投递到主队列）+ 死信队列。
	q, err := ch.QueueDeclare(c.queue, true, false, false, false, nil)
	if err != nil {
		return err
	}
	retryArgs := amqp.Table{
		"x-message-ttl":             int32(RetryTTL.Milliseconds()),
		"x-dead-letter-exchange":    ExchangeAgent,
		"x-dead-letter-routing-key": routingKey,
	}
	if _, err := ch.QueueDeclare(c.retryQueueName(), true, false, false, false, retryArgs); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(c.dlqQueueName(), true, false, false, false, nil); err != nil {
		return err
	}

	if err := ch.QueueBind(q.Name, routingKey, ExchangeAgent, false, nil); err != nil {
		return err
	}
	if err := ch.QueueBind(c.retryQueueName(), retryRoutingKey, ExchangeAgent, false, nil); err != nil {
		return err
	}
	if err := ch.QueueBind(c.dlqQueueName(), dlqRoutingKey, ExchangeAgent, false, nil); err != nil {
		return err
	}

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-msgs:
			if !ok {
				return nil
			}
			var event Event
			if err := json.Unmarshal(msg.Body, &event); err != nil {
				slog.Error("unmarshal event, route to dlq", "queue", c.queue, "error", err)
				c.routeFailed(ctx, ch, msg, dlqRoutingKey, msg.Headers)
				continue
			}
			if err := handler(ctx, event); err != nil {
				retries := retryCount(msg.Headers)
				key, next, toRetry := retryDecision(retries)
				headers := msg.Headers
				if toRetry {
					headers = cloneHeaders(msg.Headers)
					headers[RetryCountHeader] = int32(next)
				}
				slog.Warn("handler failed, route message", "routing_key", key, "retries", retries, "error", err)
				c.routeFailed(ctx, ch, msg, key, headers)
				continue
			}
			_ = msg.Ack(false)
		}
	}
}

// routeFailed 将失败消息按路由键重新发布（重试队列 / DLQ），随后 Ack 原消息。
// 只有重新发布本身失败（如 broker 异常）时才回队，避免消息丢失；
// 正常情况下失败消息不会无限 requeue。
func (c *Consumer) routeFailed(ctx context.Context, ch *amqp.Channel, msg amqp.Delivery, routingKey string, headers amqp.Table) {
	if err := ch.PublishWithContext(ctx, ExchangeAgent, routingKey, false, false, amqp.Publishing{
		ContentType:  msg.ContentType,
		DeliveryMode: msg.DeliveryMode,
		Headers:      headers,
		Body:         msg.Body,
	}); err != nil {
		slog.Error("route failed message", "routing_key", routingKey, "error", err)
		_ = msg.Nack(false, true)
		return
	}
	_ = msg.Ack(false)
}
