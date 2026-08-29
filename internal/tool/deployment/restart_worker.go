// Package deployment 提供部署处置工具（v1.0 唯一自动处置：restart_worker）。
package deployment

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	agentruntime "gooncall-agent/internal/agent/runtime"
	"gooncall-agent/internal/execution/approval"
	"gooncall-agent/internal/execution/policy"
	"gooncall-agent/internal/tool/registry"
)

// Tool 是重启 Worker 的处置工具（MEDIUM 风险，需人工审批）。
type Tool struct {
	approval *approval.Service
	policy   *policy.Engine
}

// New 创建重启工具。
func New(approvalSvc *approval.Service, policyEngine *policy.Engine) *Tool {
	return &Tool{approval: approvalSvc, policy: policyEngine}
}

// SetApproval 注入审批服务（解决与 ApprovalService 的双向依赖）。
func (t *Tool) SetApproval(svc *approval.Service) *Tool {
	t.approval = svc
	return t
}

// RestartInput 是重启入参。
type RestartInput struct {
	Target string `json:"target" jsonschema:"required,description=要重启的 worker 名称"`
	Reason string `json:"reason,omitempty" jsonschema:"description=重启原因"`
}

// RestartResult 是重启结果。
type RestartResult struct {
	Status     string `json:"status"`
	ApprovalID string `json:"approval_id,omitempty"`
	Target     string `json:"target,omitempty"`
	Message    string `json:"message,omitempty"`
}

// request 是 Agent 调用入口：发起审批，不直接执行。
func (t *Tool) request(ctx context.Context, in RestartInput) (RestartResult, error) {
	if t.policy != nil && t.policy.Evaluate(registry.RiskMedium) == policy.DecisionDeny {
		return RestartResult{}, fmt.Errorf("worker.restart denied by policy (approval required)")
	}
	if t.approval == nil {
		return RestartResult{}, fmt.Errorf("worker.restart requires approval but approval service unavailable")
	}

	args, _ := json.Marshal(in)
	runID := agentruntime.RunIDFrom(ctx)

	a, err := t.approval.Request(ctx, runID, "", "restart_worker", string(args), in.Reason)
	if err != nil {
		return RestartResult{}, err
	}
	return RestartResult{Status: "pending_approval", ApprovalID: a.ID, Target: in.Target}, nil
}

// Execute 执行重启动作（审批通过后由 ApprovalService 调用）。
func (t *Tool) Execute(_ context.Context, action, arguments string) (string, error) {
	if action != "restart_worker" {
		return "", fmt.Errorf("unknown action %q", action)
	}
	var in RestartInput
	if err := json.Unmarshal([]byte(arguments), &in); err != nil {
		return "", fmt.Errorf("parse restart arguments: %w", err)
	}

	// v1.0 模拟重启；Phase 6 接入真实部署后执行 consumer count / queue depth 验证。
	result := RestartResult{
		Status:  "completed",
		Target:  in.Target,
		Message: fmt.Sprintf("worker %s restarted", in.Target),
	}
	data, _ := json.Marshal(result)
	return string(data), nil
}

// EinoTool 返回 Eino 工具表示。
func (t *Tool) EinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"worker.restart",
		"重启指定 worker 部署以恢复消费者。MEDIUM 风险，执行前需要人工审批。",
		t.request,
	)
}
