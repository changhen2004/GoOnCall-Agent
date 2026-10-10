package bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/api/handler"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
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

// RunDiagnosis 是 Worker 的幂等诊断入口，RunWorker 与集成测试共用同一段判断
// （测试不再复制这份幂等逻辑）：
//
//   - Incident 已终态：迟到的重复消息直接成功确认；
//   - 已存在 RUNNING/COMPLETED Run：重复消息直接成功确认；
//   - Run 原子创建冲突（并发 Worker 抢先创建）：同样按重复消息成功确认，不进 DLQ。
func RunDiagnosis(ctx context.Context, svc *incidentservice.Service, runRepo repository.RunRepository, agentRT *agentruntime.Runtime, prompt, incidentID string) error {
	inc, err := svc.Get(ctx, incidentID)
	if err != nil {
		return err
	}
	if inc.Status.IsTerminal() {
		slog.Info("incident already terminal, skip diagnosis", "incident_id", inc.ID, "status", inc.Status)
		return nil
	}
	if hasActiveRun(ctx, runRepo, inc.ID) {
		slog.Info("active diagnosis run exists, skip duplicate event", "incident_id", inc.ID)
		return nil
	}
	if inc.Status == model.StatusOpen {
		if inc, err = svc.Analyze(ctx, inc.ID); err != nil {
			return err
		}
	}
	if agentRT == nil {
		slog.Warn("agent runtime not configured, skip diagnosis", "incident_id", inc.ID)
		return nil
	}
	if _, err := agentRT.Diagnose(ctx, inc, prompt); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			slog.Info("duplicate agent event, diagnosis run already active", "incident_id", inc.ID)
			return nil
		}
		return err
	}
	return nil
}

// hasActiveRun 判断 Incident 是否已有有效（RUNNING/COMPLETED）Run。
func hasActiveRun(ctx context.Context, runRepo repository.RunRepository, incidentID string) bool {
	if runRepo == nil {
		return false
	}
	runs, err := runRepo.ListRunsByIncident(ctx, incidentID)
	if err != nil {
		slog.Warn("list runs for incident", "incident_id", incidentID, "error", err)
		return false
	}
	for _, run := range runs {
		if run.Status.IsActive() {
			return true
		}
	}
	return false
}
