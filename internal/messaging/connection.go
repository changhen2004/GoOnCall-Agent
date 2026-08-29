// Package messaging 封装 RabbitMQ 连接与拓扑定义。
package messaging

import (
	amqp "github.com/rabbitmq/amqp091-go"
)

// ExchangeName 是 GoOnCall 事件交换机（topic）。
const ExchangeName = "gooncall.events"

// Connect 建立 RabbitMQ 连接。
func Connect(url string) (*amqp.Connection, error) {
	return amqp.Dial(url)
}

// DeclareExchange 声明事件交换机。
func DeclareExchange(conn *amqp.Connection) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return ch.ExchangeDeclare(
		ExchangeName,
		"topic",
		true,  // durable
		false, // auto-deleted
		false, // internal
		false, // no-wait
		nil,
	)
}
