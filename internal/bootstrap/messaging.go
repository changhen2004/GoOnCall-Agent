package bootstrap

import (
	"fmt"
	"log/slog"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/messaging"
)

// buildProducer 构建事件生产者（未配置 RabbitMQ 时返回 nil）。
func buildProducer(cfg *config.Config) (*messaging.Producer, error) {
	if cfg.RabbitMQ.URL == "" {
		slog.Warn("RABBITMQ_URL is empty, event publishing disabled")
		return nil, nil
	}
	conn, err := messaging.Connect(cfg.RabbitMQ.URL)
	if err != nil {
		return nil, fmt.Errorf("connect rabbitmq: %w", err)
	}
	return messaging.NewProducer(conn)
}
