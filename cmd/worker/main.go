// GoOnCall Agent Worker 入口（事件消费者，Phase 4 起接入 RabbitMQ）。
package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gooncall-agent/internal/config"
)

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "configs/config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("load config", "error", err, "path", cfgPath)
		os.Exit(1)
	}

	slog.Info("gooncall worker started",
		"rabbitmq_url", cfg.RabbitMQ.URL,
		"note", "事件消费者将在 Phase 4 接入 agent.queue / action.queue",
	)

	// 保持进程存活，等待优雅退出（后续阶段替换为 RabbitMQ 消费循环）。
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("gooncall worker stopped")
}
