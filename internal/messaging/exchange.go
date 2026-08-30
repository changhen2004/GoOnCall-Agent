package messaging

import amqp "github.com/rabbitmq/amqp091-go"

// ExchangeAgent 是 GoOnCall Agent 事件交换机（topic）。
const ExchangeAgent = "gooncall.agent"

// Connect 建立 RabbitMQ 连接。
func Connect(url string) (*amqp.Connection, error) {
	return amqp.Dial(url)
}

// DeclareExchange 声明 Agent 交换机。
func DeclareExchange(conn *amqp.Connection) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	return ch.ExchangeDeclare(ExchangeAgent, "topic", true, false, false, false, nil)
}
