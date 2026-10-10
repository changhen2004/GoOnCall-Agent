// Package executor 编排处置闭环：Execute -> Verify -> Resolve -> Postmortem。
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/execution/approval"
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
	verifyCfg   verifier.RunConfig
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
		verifyCfg: verifier.DefaultRunConfig(),
	}
}

// WithVerifyConfig 设置连续验证配置（attempts / interval / required successes）。
// 未调用时按单次验证执行。
func (r *Remediation) WithVerifyConfig(cfg verifier.RunConfig) *Remediation {
	r.verifyCfg = cfg
	return r
}

// RemediationResult 是处置结果。
type RemediationResult struct {
	Status     string           `json:"status"`
	Verified   bool             `json:"verified"`
	IncidentID string           `json:"incident_id,omitempty"`
	Postmortem string           `json:"postmortem,omitempty"`
	Checks     []verifier.Check `json:"checks,omitempty"`
	// OperationID 是外部处置系统返回的操作号。必须透传给 Approval.OperationID，
	// 否则真实处置的操作号只留在这一层响应里，审计链会缺一环。
	OperationID string `json:"operation_id,omitempty"`
	// Reason 是结构化失败原因（采集错误 / 验证未通过 / 连续次数不足）。
	Reason string `json:"reason,omitempty"`
	// Attempts 是实际执行的验证次数。
	Attempts int `json:"attempts,omitempty"`
}

// Execute 执行处置动作，连续验证通过后关闭 Incident 并生成复盘。
//
// ActionExecutor 契约（Agent B）：approval_id 已由 ApprovalService 通过 context 传入
// （approval.ApprovalIDFrom），HTTP 适配器用它作为 Idempotency-Key。
//
// 任何状态迁移失败都立即返回错误、停止后续步骤，绝不"忽略迁移错误继续宣称成功"。
func (r *Remediation) Execute(ctx context.Context, action, arguments, runID string) (approval.ExecutionResult, error) {
	if r.incidentSvc == nil {
		return approval.ExecutionResult{}, fmt.Errorf("no incident service configured, cannot verify or close any incident")
	}
	if r.runRepo == nil {
		return approval.ExecutionResult{}, fmt.Errorf("no run repository configured, cannot resolve incident of run %q", runID)
	}
	run, err := r.runRepo.GetRun(ctx, runID)
	if err != nil {
		return approval.ExecutionResult{}, fmt.Errorf("resolve incident of run %q: %w", runID, err)
	}
	incidentID := run.IncidentID
	if incidentID == "" {
		return approval.ExecutionResult{}, fmt.Errorf("run %q is not bound to an incident", runID)
	}

	inc, err := r.incidentSvc.Get(ctx, incidentID)
	if err != nil {
		return approval.ExecutionResult{}, err
	}
	if inc.Status.IsTerminal() {
		return approval.ExecutionResult{}, fmt.Errorf("incident %s is already %s", incidentID, inc.Status)
	}
	approvalID := approval.ApprovalIDFrom(ctx)

	if inc.Status != model.StatusMitigating {
		return approval.ExecutionResult{}, fmt.Errorf("incident %s is %s, remediation requires MITIGATING", incidentID, inc.Status)
	}

	// 1. 执行处置（Mock 或固定 URL 的 HTTP 适配器）。
	execResult, execErr := r.restart.Execute(ctx, action, arguments)
	if execErr != nil {
		if errors.Is(execErr, approval.ErrResultUnknown) {
			return approval.ExecutionResult{}, execErr
		}
		if _, failErr := r.incidentSvc.MoveToWithAudit(ctx, incidentID, model.StatusFailed, model.EventActionFailed,
			map[string]any{"approval_id": approvalID, "error": execErr.Error()}); failErr != nil {
			return approval.ExecutionResult{}, errors.Join(execErr, fmt.Errorf("move incident %s to FAILED: %w", incidentID, failErr))
		}
		return approval.ExecutionResult{}, execErr
	}
	operationID := execResult.OperationID
	if _, err := r.incidentSvc.MoveToWithAudit(ctx, incidentID, model.StatusVerifying, model.EventActionCompleted,
		map[string]any{"approval_id": approvalID, "operation_id": operationID}); err != nil {
		return approval.ExecutionResult{}, fmt.Errorf("move incident %s to VERIFYING: %w", incidentID, err)
	}

	// 2. 连续采集 + 验证。
	sequence, verifyErr := r.verifier.VerifySequence(ctx, r.gatherer, incidentID, r.verifyCfg)

	res := RemediationResult{
		IncidentID:  incidentID,
		OperationID: operationID,
		Attempts:    len(sequence.Attempts),
		Checks:      lastChecks(sequence),
		Reason:      sequence.Reason,
	}
	if verifyErr != nil {
		res.Reason = "verification interrupted: " + verifyErr.Error()
	}

	if verifyErr != nil || !sequence.Passed {
		// 采集错误 / 验证失败 -> FAILED（绝不能 RESOLVED，也不能生成"已恢复"复盘）。
		res.Status = "verify_failed"
		data, _ := json.Marshal(res)
		persistCtx := ctx
		var cancel context.CancelFunc
		if verifyErr != nil {
			persistCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
		}
		if _, err := r.incidentSvc.MoveToWithAudit(persistCtx, incidentID, model.StatusFailed, model.EventVerificationFailed,
			map[string]any{"approval_id": approvalID, "result": string(data), "operation_id": operationID, "reason": res.Reason}); err != nil {
			return approval.ExecutionResult{}, fmt.Errorf("move incident %s to FAILED: %w", incidentID, err)
		}
		r.publish(runID, "incident.verify_failed", map[string]any{"incident_id": incidentID, "reason": res.Reason})
		return approval.ExecutionResult{Output: string(data), OperationID: operationID, IncidentID: incidentID}, nil
	}

	// 3. 连续验证通过 -> RESOLVED + 生成复盘（输入全部来自真实执行/验证结果）。
	res.Status = "completed"
	res.Verified = true
	if r.postmortem != nil {
		res.Postmortem = r.postmortem.Generate(
			inc,
			r.rootCause(ctx, runID),
			checkDetails(sequence),
			fmt.Sprintf("%s operation_id=%s；连续验证通过 %d 次（共 %d 次采集）",
				action, operationID, sequence.Successes, len(sequence.Attempts)),
		)
	}
	data, _ := json.Marshal(res)
	if _, err := r.incidentSvc.MoveToWithAudit(ctx, incidentID, model.StatusResolved, model.EventPostmortemGenerated,
		map[string]any{"approval_id": approvalID, "result": string(data), "postmortem": res.Postmortem, "operation_id": operationID}); err != nil {
		return approval.ExecutionResult{}, fmt.Errorf("move incident %s to RESOLVED: %w", incidentID, err)
	}
	r.publish(runID, "incident.resolved", map[string]any{"incident_id": incidentID})
	return approval.ExecutionResult{
		Output: string(data), OperationID: operationID,
		IncidentID: incidentID, Verified: true,
	}, nil
}

// rootCause 取本次 Run 记录的最终诊断结论（Recorder 落库的 finalize 步骤真实输出）。
// 取不到时如实标注，绝不编造根因。
func (r *Remediation) rootCause(ctx context.Context, runID string) string {
	steps, err := r.runRepo.ListSteps(ctx, runID)
	if err != nil {
		return "未记录诊断结论（读取 Run 步骤失败）"
	}
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Action == "finalize" && strings.TrimSpace(steps[i].Output) != "" {
			return steps[i].Output
		}
	}
	return "未记录诊断结论"
}

func (r *Remediation) publish(runID, typ string, data map[string]any) {
	if r.broker != nil {
		r.broker.Publish(runID, agentruntime.StreamEvent{Type: typ, Data: data})
	}
}

// lastChecks 返回最后一次采集得到的检查明细（用于复盘与响应）。
func lastChecks(sequence verifier.SequenceResult) []verifier.Check {
	for i := len(sequence.Attempts) - 1; i >= 0; i-- {
		if len(sequence.Attempts[i].Checks) > 0 {
			return sequence.Attempts[i].Checks
		}
	}
	return nil
}

func checkDetails(sequence verifier.SequenceResult) []string {
	out := make([]string, 0)
	for _, attempt := range sequence.Attempts {
		prefix := fmt.Sprintf("attempt %d", attempt.Attempt)
		if attempt.Error != "" {
			out = append(out, fmt.Sprintf("%s: gather error: %s", prefix, attempt.Error))
			continue
		}
		for _, c := range attempt.Checks {
			out = append(out, fmt.Sprintf("%s: %s (passed=%t)", prefix, c.Detail, c.Passed))
		}
	}
	return out
}
