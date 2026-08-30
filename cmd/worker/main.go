// GoOnCall Agent Worker：消费 RabbitMQ 事件并运行 Agent。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/messaging"
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

	conn, err := messaging.Connect(cfg.RabbitMQ.URL)
	if err != nil {
		slog.Error("connect rabbitmq", "error", err, "url", cfg.RabbitMQ.URL)
		os.Exit(1)
	}
	defer conn.Close()

	consumer := messaging.NewConsumer(conn, "gooncall.agent.queue")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("gooncall worker started", "routing_key", messaging.AgentRequested)

	if err := consumer.Subscribe(ctx, messaging.AgentRequested, handleAgentRequested); err != nil {
		slog.Error("consumer error", "error", err)
		os.Exit(1)
	}
	slog.Info("gooncall worker stopped")
}

// handleAgentRequested 处理 agent.requested 事件。
// Stage 4（Bootstrap 重构）后接入完整 Agent Runtime：runtime.Run(ctx, event)。
func handleAgentRequested(ctx context.Context, event messaging.Event) error {
	var payload struct {
		IncidentID string `json:"incident_id"`
	}
	if err := event.DecodePayload(&payload); err != nil {
		slog.Warn("decode agent.requested payload", "error", err, "event_id", event.ID)
		return err
	}
	slog.Info("agent.requested", "event_id", event.ID, "incident_id", payload.IncidentID)
	// TODO(Stage 4): 加载 Incident 并运行诊断 Agent。
	return nil
}
