package bootstrap

import (
	"log/slog"
	"os"
	"time"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/incident/repository"
	"gooncall-agent/internal/tool/registry"
)

const diagnosisPromptPath = "prompts/diagnosis/system.md"

// buildAgent 构建诊断 Agent（未配置 LLM 时返回 nil）。
func buildAgent(cfg *config.Config, reg *registry.Registry, runRepo repository.RunRepository, broker *agentruntime.StreamBroker) (*agentruntime.Runtime, handler.Diagnoser, string) {
	if cfg.LLM.BaseURL == "" || cfg.LLM.Model == "" {
		slog.Warn("LLM not configured (LLM_BASE_URL / LLM_MODEL empty), analyze will skip diagnosis")
		return nil, nil, ""
	}

	chatModel := agentruntime.NewOpenAIChatModel(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.Model)
	rt := agentruntime.New(chatModel, reg, cfg.Agent.MaxSteps).
		WithRunRecording(runRepo, broker).
		WithToolLimits(time.Duration(cfg.Tool.TimeoutSeconds)*time.Second, cfg.Agent.MaxToolCalls).
		WithRunTimeout(time.Duration(cfg.Agent.TimeoutSeconds) * time.Second)
	prompt := loadPrompt(diagnosisPromptPath)
	slog.Info("agent runtime enabled", "model", cfg.LLM.Model, "tools", reg.Names())
	return rt, rt, prompt
}

func loadPrompt(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("load prompt", "error", err, "path", path)
		return "你是运维故障诊断 Agent。"
	}
	return string(data)
}
