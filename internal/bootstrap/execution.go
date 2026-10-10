package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/execution/executor"
	"gooncall-agent/internal/execution/policy"
	"gooncall-agent/internal/execution/postmortem"
	"gooncall-agent/internal/execution/verifier"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/observability/metrics"
	"gooncall-agent/internal/tool/deployment"
)

// buildGatherer 按配置切换 Mock / Prometheus 验证采集器（PromQL 从配置注入，适配业务系统指标名）。
func buildGatherer(cfg *config.Config) (verifier.MetricGatherer, error) {
	switch cfg.Verification.Mode {
	case "prometheus":
		return verifier.NewPrometheusGatherer(cfg.Prometheus.URL, verifier.Queries{
			Consumers:  cfg.Verification.Queries.Consumers,
			QueueDepth: cfg.Verification.Queries.QueueDepth,
			ErrorRate:  cfg.Verification.Queries.ErrorRate,
		}), nil
	default:
		// 真实处置路径不允许用 Mock 验证关闭 Incident（config.Validate 已拦截，这里再兜一层）。
		if cfg.Execution.Mode == config.ExecutionModeHTTP {
			return nil, fmt.Errorf(
				"execution.mode=%s must not use the mock verification gatherer: a mock result must never close a real incident",
				config.ExecutionModeHTTP)
		}
		return verifier.NewMockGatherer(), nil
	}
}

// buildExecution 按配置装配策略、真实处置通道、连续验证与审批服务。
func buildExecution(cfg *config.Config, app *App) (*deployment.Tool, error) {
	policyEngine := policy.New(cfg.Approval.Enabled)
	restartTool := deployment.New(nil, policyEngine)

	if cfg.Execution.Mode == config.ExecutionModeHTTP {
		executorHTTP, err := deployment.NewHTTPExecutor(deployment.HTTPExecutorConfig{
			URL:     cfg.Execution.RestartURL,
			Token:   cfg.Execution.Token,
			Timeout: time.Duration(cfg.Execution.TimeoutSeconds) * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("build http executor: %w", err)
		}
		// 真实处置必须显式给出 target 白名单，否则 deployment 会拒绝所有 target。
		restartTool.WithHTTPExecutor(executorHTTP).WithAllowedTargets(cfg.Execution.AllowedTargets)
	}

	gatherer, err := buildGatherer(cfg)
	if err != nil {
		return nil, err
	}

	remediation := executor.NewRemediation(
		restartTool,
		verifier.New(verifier.DefaultConfig()),
		gatherer,
		app.incidentSvc,
		app.runRepo,
		postmortem.New(),
		app.broker,
	).WithVerifyConfig(verifier.RunConfig{
		Attempts:          cfg.Verification.Attempts,
		Interval:          time.Duration(cfg.Verification.IntervalSeconds) * time.Second,
		RequiredSuccesses: cfg.Verification.RequiredSuccesses,
	})

	app.approvalSvc = approval.New(app.approvalRepo, app.broker, observedExecutor{
		inner:       remediation,
		incidentSvc: app.incidentSvc,
	}).WithIncidentState(app.incidentSvc, app.runRepo)
	restartTool.SetApproval(app.approvalSvc)

	// 外部恢复（Alertmanager resolved）必须先让待审批失效，再关闭 Incident。
	app.incidentSvc.WithApprovalExpirer(app.approvalSvc)
	return restartTool, nil
}

// observedExecutor 记录执行 / 验证结果与闭环耗时指标。
//
// 它只观察，不改变 ActionExecutor 契约：编排逻辑仍然完整属于 executor.Remediation。
type observedExecutor struct {
	inner       approval.ActionExecutor
	incidentSvc *incidentservice.Service
}

// Execute 调用真实编排器并统计结果。
func (o observedExecutor) Execute(ctx context.Context, action, arguments, runID string) (approval.ExecutionResult, error) {
	result, err := o.inner.Execute(ctx, action, arguments, runID)
	switch {
	case err == nil:
		metrics.ExecutionsTotal.WithLabelValues(action, "completed").Inc()
	case errors.Is(err, approval.ErrResultUnknown):
		// 超时/结果未知：既不是成功也不是明确失败，禁止自动重试。
		metrics.ExecutionsTotal.WithLabelValues(action, "unknown").Inc()
	default:
		metrics.ExecutionsTotal.WithLabelValues(action, "failed").Inc()
	}
	if err != nil {
		return result, err
	}

	if result.Verified {
		metrics.VerificationsTotal.WithLabelValues(action, "verified").Inc()
		o.observeClosure(ctx, action, result.IncidentID)
	} else {
		metrics.VerificationsTotal.WithLabelValues(action, "verify_failed").Inc()
	}
	return result, nil
}

// observeClosure 在闭环成功关闭后观测从 Incident 开始到 RESOLVED 的总耗时。
func (o observedExecutor) observeClosure(ctx context.Context, action, incidentID string) {
	if o.incidentSvc == nil || incidentID == "" {
		return
	}
	inc, err := o.incidentSvc.Get(ctx, incidentID)
	if err != nil || inc.ResolvedAt == nil {
		return
	}
	metrics.ClosureDurationSeconds.WithLabelValues(action).Observe(inc.ResolvedAt.Sub(inc.StartedAt).Seconds())
}
