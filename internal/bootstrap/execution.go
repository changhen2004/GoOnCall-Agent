package bootstrap

import (
	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/config"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/execution/executor"
	"gooncall-agent/internal/execution/policy"
	"gooncall-agent/internal/execution/postmortem"
	"gooncall-agent/internal/execution/verifier"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/tool/deployment"
)

// buildGatherer 按配置切换 Mock / Prometheus 验证采集器（PromQL 从配置注入，适配业务系统指标名）。
func buildGatherer(cfg *config.Config) verifier.MetricGatherer {
	switch cfg.Verification.Mode {
	case "prometheus":
		return verifier.NewPrometheusGatherer(cfg.Prometheus.URL, verifier.Queries{
			Consumers:  cfg.Verification.Queries.Consumers,
			QueueDepth: cfg.Verification.Queries.QueueDepth,
			ErrorRate:  cfg.Verification.Queries.ErrorRate,
		})
	default:
		return verifier.NewMockGatherer()
	}
}

// buildExecution 构建策略、处置编排与审批服务。
func buildExecution(cfg *config.Config, svc *incidentservice.Service, runRepo repository.RunRepository, approvalRepo repository.ApprovalRepository, broker *agentruntime.StreamBroker) (*approval.Service, *deployment.Tool) {
	policyEngine := policy.New(cfg.Approval.Enabled)
	restartTool := deployment.New(nil, policyEngine)

	remediation := executor.NewRemediation(
		restartTool,
		verifier.New(verifier.DefaultConfig()),
		buildGatherer(cfg),
		svc,
		runRepo,
		postmortem.New(),
		broker,
	)
	approvalSvc := approval.New(approvalRepo, broker, remediation).WithIncidentState(svc, runRepo)
	restartTool.SetApproval(approvalSvc)
	return approvalSvc, restartTool
}
