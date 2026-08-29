// Package state_machine 定义并校验 Incident 状态机。
//
// 状态迁移必须由 Go Service 控制，LLM 不允许直接修改状态（设计文档 3.1 / 17 节）。
package state_machine

import (
	"fmt"

	"gooncall-agent/internal/incident/model"
)

// transitions 是允许的状态迁移表。终态（RESOLVED/FAILED/CANCELLED）无出边。
var transitions = map[model.Status][]model.Status{
	model.StatusOpen: {
		model.StatusInvestigating,
		model.StatusCancelled,
	},
	model.StatusInvestigating: {
		model.StatusNeedApproval,
		model.StatusResolved,
		model.StatusFailed,
		model.StatusCancelled,
	},
	model.StatusNeedApproval: {
		model.StatusWaitingApproval,
		model.StatusCancelled,
	},
	model.StatusWaitingApproval: {
		model.StatusMitigating,
		model.StatusFailed,
		model.StatusCancelled,
	},
	model.StatusMitigating: {
		model.StatusVerifying,
		model.StatusFailed,
	},
	model.StatusVerifying: {
		model.StatusResolved,
		model.StatusInvestigating,
		model.StatusFailed,
	},
}

// TransitionError 表示一次非法的状态迁移。
type TransitionError struct {
	From model.Status
	To   model.Status
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("invalid incident state transition: %s -> %s", e.From, e.To)
}

// AllowedTargets 返回 from 状态允许迁移到的目标状态集合。
func AllowedTargets(from model.Status) []model.Status {
	return transitions[from]
}

// CanTransition 判断 from 能否迁移到 to。
func CanTransition(from, to model.Status) bool {
	for _, target := range transitions[from] {
		if target == to {
			return true
		}
	}
	return false
}

// Transition 校验一次状态迁移，非法时返回 *TransitionError。
func Transition(from, to model.Status) error {
	if CanTransition(from, to) {
		return nil
	}
	return &TransitionError{From: from, To: to}
}
