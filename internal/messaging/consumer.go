package messaging

import (
	"context"
	"encoding/json"

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
func (c *Consumer) Subscribe(ctx context.Context, routingKey string, handler Handler) error {
	ch, err := c.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(c.queue, true, false, false, false, nil)
	if err != nil {
		return err
	}
	if err := ch.QueueBind(q.Name, routingKey, ExchangeAgent, false, nil); err != nil {
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
				_ = msg.Nack(false, false)
				continue
			}
			if err := handler(ctx, event); err != nil {
				_ = msg.Nack(false, true)
				continue
			}
			_ = msg.Ack(false)
		}
	}
}
