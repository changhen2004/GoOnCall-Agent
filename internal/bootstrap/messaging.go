package bootstrap

import (
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/messaging"
)

// buildProducer 构建事件生产者（未配置 RabbitMQ 时全部返回 nil）。
//
// 同时返回连接本身：就绪检查需要判断 RabbitMQ 是否仍可用。
func buildProducer(cfg *config.Config) (*messaging.Producer, *amqp.Connection, error) {
	if cfg.RabbitMQ.URL == "" {
		slog.Warn("RABBITMQ_URL is empty, event publishing disabled")
		return nil, nil, nil
	}
	conn, err := messaging.Connect(cfg.RabbitMQ.URL)
	if err != nil {
		return nil, nil, fmt.Errorf("connect rabbitmq: %w", err)
	}
	producer, err := messaging.NewProducer(conn)
	if err != nil {
		return nil, nil, fmt.Errorf("declare agent queue: %w", err)
	}
	return producer, conn, nil
}
