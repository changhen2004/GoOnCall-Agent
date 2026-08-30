package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// 发布错误，供上层判断投递失败原因。
var (
	// ErrPublishNotConfirmed 表示 broker 未确认（nack）消息。
	ErrPublishNotConfirmed = errors.New("publish not confirmed by rabbitmq broker")
	// ErrPublishUnroutable 表示消息不可路由（无队列绑定该路由键），消息会被丢弃。
	ErrPublishUnroutable = errors.New("publish unroutable: no queue bound to routing key")
)

// Producer 发布领域事件到 Agent 交换机。
type Producer struct {
	conn *amqp.Connection
}

// NewProducer 创建 Producer：幂等声明交换机 + 主队列 + agent.requested 绑定。
// 发布端先声明拓扑，保证 Worker 晚启动时事件仍可路由（排队等待消费，不丢事件）。
func NewProducer(conn *amqp.Connection) (*Producer, error) {
	if err := EnsureRoute(conn, QueueAgent, AgentRequested); err != nil {
		return nil, err
	}
	return &Producer{conn: conn}, nil
}

// Publish 以 publisher confirm 模式发布事件到指定路由键。
//
// 开启 channel confirm：等待 broker 确认（消息已接受并持久化）后才返回成功，
// broker 拒绝（nack）时返回 ErrPublishNotConfirmed，ctx 取消时返回 ctx.Err()。
// mandatory 模式下，无队列绑定该路由键的消息会被退回发布者并返回
// ErrPublishUnroutable，避免消息被静默丢弃。
func (p *Producer) Publish(ctx context.Context, routingKey string, event Event) error {
	if p.conn == nil {
		return errors.New("producer: nil connection")
	}

	ch, err := p.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.Confirm(false); err != nil {
		return fmt.Errorf("enable publisher confirm: %w", err)
	}
	confirms := ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))

	body, err := json.Marshal(event)
	if err != nil {
		return err
	}

	if err := ch.PublishWithContext(ctx,
		ExchangeAgent,
		routingKey,
		true,  // mandatory: 不可路由时退回发布者
		false, // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         body,
		},
	); err != nil {
		return err
	}

	select {
	case conf := <-confirms:
		if !conf.Ack {
			return ErrPublishNotConfirmed
		}
	case <-ctx.Done():
		return ctx.Err()
	}

	// 不可路由的 mandatory 消息：broker 先发 basic.return 再确认，
	// 收到确认后非阻塞检查是否同时存在退回消息。
	select {
	case r := <-returns:
		return fmt.Errorf("%w: routing_key=%s (%s)", ErrPublishUnroutable, r.RoutingKey, r.ReplyText)
	default:
	}
	return nil
}
