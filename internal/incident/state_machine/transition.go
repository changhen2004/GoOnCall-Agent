// Package state_machine 定义并校验 Incident 状态机。
//
// v1.1 采用严格状态闭环：状态迁移必须由 Go Service 控制，LLM 不允许直接修改状态。
package state_machine

import (
	"fmt"

	"gooncall-agent/internal/incident/model"
)

// transitions 是 v1.1 严格状态迁移表。
// 只允许主流程：OPEN -> INVESTIGATING -> WAITING_APPROVAL -> MITIGATING -> VERIFYING -> RESOLVED。
// 明确禁止 INVESTIGATING -> RESOLVED（必须经过审批、处置与验证）。
var transitions = map[model.Status][]model.Status{
	model.StatusOpen:            {model.StatusInvestigating},
	model.StatusInvestigating:   {model.StatusWaitingApproval},
	model.StatusWaitingApproval: {model.StatusMitigating, model.StatusFailed},
	model.StatusMitigating:      {model.StatusVerifying, model.StatusFailed},
	model.StatusVerifying:       {model.StatusResolved, model.StatusFailed},
}

// TransitionError 表示一次非法的状态迁移。
type TransitionError struct {
	From model.Status
	To   model.Status
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("invalid incident state transition: %s -> %s", e.From, e.To)
}

// AllowedTargets 返回 current 状态允许迁移到的目标状态集合。
func AllowedTargets(current model.Status) []model.Status {
	return transitions[current]
}

// CanTransition 判断 current 能否迁移到 next。
func CanTransition(current, next model.Status) bool {
	for _, target := range transitions[current] {
		if target == next {
			return true
		}
	}
	return false
}

// Transition 校验一次状态迁移，非法时返回 *TransitionError。
func Transition(current, next model.Status) error {
	if CanTransition(current, next) {
		return nil
	}
	return &TransitionError{From: current, To: next}
}
