// GoOnCall Agent Worker 入口：消费 RabbitMQ 事件并运行 Agent。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gooncall-agent/internal/bootstrap"
)

func main() {
	app, err := bootstrap.New()
	if err != nil {
		slog.Error("bootstrap", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := app.RunWorker(ctx); err != nil {
		slog.Error("worker error", "error", err)
		os.Exit(1)
	}
}
