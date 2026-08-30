package messaging

import (
	"context"
	"encoding/json"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Producer 发布领域事件到 Agent 交换机。
type Producer struct {
	conn *amqp.Connection
}

// NewProducer 创建 Producer 并声明交换机。
func NewProducer(conn *amqp.Connection) (*Producer, error) {
	if err := DeclareExchange(conn); err != nil {
		return nil, err
	}
	return &Producer{conn: conn}, nil
}

// Publish 发布事件到指定路由键。
func (p *Producer) Publish(ctx context.Context, routingKey string, event Event) error {
	ch, err := p.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return ch.PublishWithContext(ctx,
		ExchangeAgent,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	)
}
