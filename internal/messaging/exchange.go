// Package messaging 提供 RabbitMQ 连接与事件拓扑声明。
package messaging

import amqp "github.com/rabbitmq/amqp091-go"

// ExchangeAgent 是 GoOnCall Agent 事件交换机（topic）。
const ExchangeAgent = "gooncall.agent"

// QueueAgent 是 Agent 事件主队列（Worker 消费 agent.requested）。
const QueueAgent = "gooncall.agent.queue"

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

// EnsureRoute 幂等声明交换机 + 主队列 + 路由绑定。
//
// 供 Producer 与 Consumer 共用：发布端启动时先声明，保证 Worker 尚未启动时
// agent.requested 也不会因 NO_ROUTE 被丢弃——消息会在主队列中排队，等待
// Worker 消费（Worker 晚于 API 启动的部署顺序因此安全）。
func EnsureRoute(conn *amqp.Connection, queue, routingKey string) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.ExchangeDeclare(ExchangeAgent, "topic", true, false, false, false, nil); err != nil {
		return err
	}
	q, err := ch.QueueDeclare(queue, true, false, false, false, nil)
	if err != nil {
		return err
	}
	return ch.QueueBind(q.Name, routingKey, ExchangeAgent, false, nil)
}
