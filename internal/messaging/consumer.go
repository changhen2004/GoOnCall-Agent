package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"gooncall-agent/internal/observability/metrics"
)

// Handler 处理事件。
type Handler func(ctx context.Context, event Event) error

// TerminalHandler 是重试耗尽（消息已确认进入 DLQ）时的终态回调，
// 用于把对应 Incident 置为 FAILED 并记录最后错误，避免告警永久停在非终态。
// 返回错误只记录日志：死信证据已经落盘，不能因为收尾失败而回滚。
type TerminalHandler func(ctx context.Context, event Event, cause error) error

// Consumer 订阅事件并处理。
type Consumer struct {
	conn     *amqp.Connection
	queue    string
	terminal TerminalHandler
}

// NewConsumer 创建 Consumer。
func NewConsumer(conn *amqp.Connection, queue string) *Consumer {
	return &Consumer{conn: conn, queue: queue}
}

// WithTerminalHandler 注入重试耗尽的终态回调。
func (c *Consumer) WithTerminalHandler(h TerminalHandler) *Consumer {
	c.terminal = h
	return c
}

// Subscribe 订阅指定路由键，逐条处理事件。ctx 取消时返回。
//
// 消费失败不会无限 requeue：失败消息按 x-retry-count 路由到重试队列
// （TTL 到期后重新投递），超过 MaxRetries 次后进入死信队列（DLQ）。
//
// 投递语义：重投（retry / DLQ）走 publisher confirm，只有 broker 确认落盘后才 Ack 原消息；
// 确认失败则 Nack(requeue) 保留原消息。这样不会出现"DLQ 没写成功、原消息被 Ack"的静默丢失。
func (c *Consumer) Subscribe(ctx context.Context, routingKey string, handler Handler) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// 开启 channel confirm：重投必须等 broker 确认（PublishWithDeferredConfirm 依赖它）。
	if err := ch.Confirm(false); err != nil {
		return fmt.Errorf("enable publisher confirm: %w", err)
	}

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
				if rerr := c.republishConfirmed(ctx, ch, msg, dlqRoutingKey, msg.Headers); rerr != nil {
					slog.Error("route undecodable message to dlq", "error", rerr)
				} else {
					recordRoutedMessage(true)
					_ = msg.Ack(false)
				}
				continue
			}
			if err := handler(ctx, event); err != nil {
				key, headers, toDLQ := failedMessageRoute(msg.Headers)
				slog.Warn("handler failed, route message", "routing_key", key, "retries", retryCount(msg.Headers), "error", err)
				if rerr := c.republishConfirmed(ctx, ch, msg, key, headers); rerr != nil {
					slog.Error("route failed message", "routing_key", key, "error", rerr)
					continue
				}
				recordRoutedMessage(toDLQ)
				// 只有 DLQ 已经被 broker 确认，才结束 Incident：
				// 反过来（先 FAILED 再投递）会在重投失败时留下"Incident 已终态、消息还在队列"的假死。
				if toDLQ {
					c.finishDeadLetter(ctx, msg, event, err)
				} else {
					_ = msg.Ack(false)
				}
				continue
			}
			_ = msg.Ack(false)
		}
	}
}

func recordRoutedMessage(toDLQ bool) {
	if toDLQ {
		metrics.DLQTotal.Inc()
		return
	}
	metrics.RetriesTotal.Inc()
}

// republishConfirmed 以 publisher confirm 模式重投消息（重试队列 / DLQ）。
// 确认失败或 ctx 取消则 Nack(requeue) 保留原消息；成功后的 Ack 由调用方在收尾完成后执行。
func (c *Consumer) republishConfirmed(ctx context.Context, ch *amqp.Channel, msg amqp.Delivery, routingKey string, headers amqp.Table) error {
	confirmation, err := ch.PublishWithDeferredConfirm(ExchangeAgent, routingKey, false, false, amqp.Publishing{
		ContentType:  msg.ContentType,
		DeliveryMode: msg.DeliveryMode,
		Headers:      headers,
		Body:         msg.Body,
	})
	if err != nil {
		_ = msg.Nack(false, true)
		return fmt.Errorf("republish to %s: %w", routingKey, err)
	}
	if confirmation == nil {
		// channel 不在 confirm 模式：无法证明重投已落盘，宁可不 Ack 原消息。
		_ = msg.Nack(false, true)
		return errors.New("publisher confirm is not enabled on the consumer channel")
	}
	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		_ = msg.Nack(false, true)
		return fmt.Errorf("wait for confirm of republish to %s: %w", routingKey, err)
	}
	if !acked {
		_ = msg.Nack(false, true)
		return fmt.Errorf("republish to %s was nacked by the broker", routingKey)
	}
	return nil
}

const terminalHandlerTimeout = 5 * time.Second

// finishDeadLetter 在 DLQ 已确认后先完成终态回调，再 Ack 原消息。
func (c *Consumer) finishDeadLetter(ctx context.Context, msg amqp.Delivery, event Event, cause error) {
	terminalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalHandlerTimeout)
	defer cancel()
	c.onDeadLetter(terminalCtx, event, cause)
	_ = msg.Ack(false)
}

// onDeadLetter 执行终态回调；回调失败只记录日志。
func (c *Consumer) onDeadLetter(ctx context.Context, event Event, cause error) {
	if c.terminal == nil {
		return
	}
	if err := c.terminal(ctx, event, cause); err != nil {
		slog.Error("terminal handler failed", "event_id", event.ID, "error", err)
	}
}
