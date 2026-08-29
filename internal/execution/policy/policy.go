// Package policy 提供工具风险策略引擎（设计文档 8.3 / 8.4）。
package policy

import "gooncall-agent/internal/tool/registry"

// Decision 是风险策略决策结果。
type Decision string

const (
	DecisionAllow           Decision = "ALLOW"
	DecisionRequireApproval Decision = "REQUIRE_APPROVAL"
	DecisionDeny            Decision = "DENY"
)

// Engine 是风险策略引擎。
type Engine struct {
	approvalEnabled bool
}

// New 创建策略引擎。approvalEnabled 表示人工审批是否启用。
func New(approvalEnabled bool) *Engine {
	return &Engine{approvalEnabled: approvalEnabled}
}

// Evaluate 根据风险等级返回处置决策。
//
//	LOW      -> 允许直接执行
//	MEDIUM/HIGH -> 需要人工审批（审批未启用时拒绝）
//	CRITICAL -> v1.0 禁止执行
func (e *Engine) Evaluate(risk registry.RiskLevel) Decision {
	switch risk {
	case registry.RiskLow:
		return DecisionAllow
	case registry.RiskMedium, registry.RiskHigh:
		if e.approvalEnabled {
			return DecisionRequireApproval
		}
		return DecisionDeny
	case registry.RiskCritical:
		return DecisionDeny
	default:
		return DecisionDeny
	}
}

// RequireApproval 判断风险等级是否需要人工审批。
func (e *Engine) RequireApproval(risk registry.RiskLevel) bool {
	return e.Evaluate(risk) == DecisionRequireApproval
}
