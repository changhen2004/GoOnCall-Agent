// Package deployment 提供部署处置工具（v1.0 唯一自动处置：restart_worker）。
package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	// allowedTargets 为 nil 表示未配置白名单（Mock/Demo 模式）；
	// 非 nil 时只允许列表内的 target。HTTP 执行模式必须由 Bootstrap 配置。
	allowedTargets []string
	http           *HTTPExecutor
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

// WithAllowedTargets 设置 target 白名单（HTTP 执行模式必须配置；传空切片表示禁止任何 target）。
func (t *Tool) WithAllowedTargets(targets []string) *Tool {
	t.allowedTargets = targets
	return t
}

// WithHTTPExecutor 用固定 URL 的 HTTP 适配器替换模拟执行。
func (t *Tool) WithHTTPExecutor(e *HTTPExecutor) *Tool {
	t.http = e
	return t
}

// checkTarget 校验 target：空值一律拒绝；配置了白名单时只允许列表内的成员。
//
// 接入 HTTP executor 后不存在"未配置就放行"：真实处置必须显式给出白名单，
// 否则 LLM 可以用任意 target 触发真实重启。
func (t *Tool) checkTarget(target string) error {
	if strings.TrimSpace(target) == "" {
		return errors.New("worker.restart target is required")
	}
	if t.allowedTargets == nil {
		if t.http != nil {
			return errors.New("worker.restart target whitelist is not configured, refusing to call the deployment endpoint")
		}
		return nil
	}
	if len(t.allowedTargets) == 0 {
		return errors.New("worker.restart target whitelist is empty, no target is allowed")
	}
	for _, allowed := range t.allowedTargets {
		if allowed == target {
			return nil
		}
	}
	return fmt.Errorf("worker.restart target %q is not in the allowed targets", target)
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
	// 请求审批前先校验 target：LLM 不能通过任意 target 扩大处置范围。
	if err := t.checkTarget(in.Target); err != nil {
		return RestartResult{}, err
	}

	args, _ := json.Marshal(in)
	runID := agentruntime.RunIDFrom(ctx)
	// ToolCall ID 由 Recorder 在调用工具前生成并经 context 传入：
	// ToolCall 记录与 Approval 必须引用同一个 ID，否则审计链断裂。
	toolCallID := agentruntime.ToolCallIDFrom(ctx)

	a, err := t.approval.Request(ctx, runID, toolCallID, "restart_worker", string(args), in.Reason)
	if err != nil {
		return RestartResult{}, err
	}
	return RestartResult{Status: "pending_approval", ApprovalID: a.ID, Target: in.Target}, nil
}

// Execute 执行重启动作（审批通过后由 ApprovalService 调用；approval_id 幂等键经 context 传入）。
func (t *Tool) Execute(ctx context.Context, action, arguments string) (approval.ExecutionResult, error) {
	if action != "restart_worker" {
		return approval.ExecutionResult{}, fmt.Errorf("unknown action %q", action)
	}
	var in RestartInput
	if err := json.Unmarshal([]byte(arguments), &in); err != nil {
		return approval.ExecutionResult{}, fmt.Errorf("parse restart arguments: %w", err)
	}
	// 执行前再校验一次：审批可能在白名单变更之前创建。
	if err := t.checkTarget(in.Target); err != nil {
		return approval.ExecutionResult{}, err
	}
	if t.http != nil {
		return t.http.Execute(ctx, action, arguments)
	}

	// Mock 模式模拟重启；HTTP 模式由 HTTPExecutor 调用固定 endpoint。
	result := RestartResult{
		Status:  "completed",
		Target:  in.Target,
		Message: fmt.Sprintf("worker %s restarted", in.Target),
	}
	data, _ := json.Marshal(result)
	return approval.ExecutionResult{Output: string(data)}, nil
}

// EinoTool 返回 Eino 工具表示。
func (t *Tool) EinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"worker_restart",
		"重启指定 worker 部署以恢复消费者。MEDIUM 风险，执行前需要人工审批。",
		t.request,
	)
}
