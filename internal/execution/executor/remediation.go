// Package executor 编排处置闭环：Execute -> Verify -> Resolve -> Postmortem。
package executor

import (
	"context"
	"encoding/json"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/execution/postmortem"
	"gooncall-agent/internal/execution/verifier"
	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
	incidentservice "gooncall-agent/internal/incident/service"
	"gooncall-agent/internal/tool/deployment"
)

// Remediation 编排处置闭环。
type Remediation struct {
	restart     *deployment.Tool
	verifier    *verifier.Verifier
	gatherer    verifier.MetricGatherer
	incidentSvc *incidentservice.Service
	runRepo     repository.RunRepository
	postmortem  *postmortem.Generator
	broker      *agentruntime.StreamBroker
}

// NewRemediation 创建处置编排器。
func NewRemediation(
	restart *deployment.Tool,
	v *verifier.Verifier,
	gatherer verifier.MetricGatherer,
	incidentSvc *incidentservice.Service,
	runRepo repository.RunRepository,
	pm *postmortem.Generator,
	broker *agentruntime.StreamBroker,
) *Remediation {
	return &Remediation{
		restart: restart, verifier: v, gatherer: gatherer,
		incidentSvc: incidentSvc, runRepo: runRepo, postmortem: pm, broker: broker,
	}
}

// RemediationResult 是处置结果。
type RemediationResult struct {
	Status     string           `json:"status"`
	Verified   bool             `json:"verified"`
	IncidentID string           `json:"incident_id,omitempty"`
	Postmortem string           `json:"postmortem,omitempty"`
	Checks     []verifier.Check `json:"checks,omitempty"`
}

// Execute 执行处置动作，验证后自动关闭 Incident 并生成复盘。
func (r *Remediation) Execute(ctx context.Context, action, arguments, runID string) (string, error) {
	// 1. 执行重启
	if _, err := r.restart.Execute(ctx, action, arguments); err != nil {
		return "", err
	}

	// 2. 根据 run 定位 incident
	incidentID := ""
	if r.runRepo != nil && runID != "" {
		if run, err := r.runRepo.GetRun(ctx, runID); err == nil {
			incidentID = run.IncidentID
		}
	}

	// 3. 进入验证阶段：MITIGATING -> VERIFYING
	if incidentID != "" && r.incidentSvc != nil {
		_, _ = r.incidentSvc.MoveTo(ctx, incidentID, model.StatusVerifying)
	}

	// 4. 采集指标 + 验证
	metrics := verifier.Metrics{}
	if r.gatherer != nil && incidentID != "" {
		if m, err := r.gatherer.Gather(ctx, incidentID); err == nil {
			metrics = m
		}
	}
	result := r.verifier.Verify(metrics)

	// 5. 验证通过 -> RESOLVED + 生成复盘；失败 -> FAILED
	res := RemediationResult{Status: "completed", Verified: result.Passed, Checks: result.Checks}
	if result.Passed && incidentID != "" && r.incidentSvc != nil {
		if resolved, err := r.incidentSvc.MoveTo(ctx, incidentID, model.StatusResolved); err == nil {
			res.IncidentID = incidentID
			if r.postmortem != nil {
				res.Postmortem = r.postmortem.Generate(resolved, "worker restart 后指标恢复", checkDetails(result.Checks), "restart worker")
			}
			r.publish(runID, "incident.resolved", map[string]any{"incident_id": incidentID})
		}
	} else if !result.Passed && incidentID != "" && r.incidentSvc != nil {
		res.Status = "verify_failed"
		_, _ = r.incidentSvc.MoveTo(ctx, incidentID, model.StatusFailed)
		r.publish(runID, "incident.verify_failed", map[string]any{"incident_id": incidentID})
	}

	data, _ := json.Marshal(res)
	return string(data), nil
}

func (r *Remediation) publish(runID, typ string, data map[string]any) {
	if r.broker != nil {
		r.broker.Publish(runID, agentruntime.StreamEvent{Type: typ, Data: data})
	}
}

func checkDetails(checks []verifier.Check) []string {
	out := make([]string, 0, len(checks))
	for _, c := range checks {
		out = append(out, c.Detail)
	}
	return out
}
