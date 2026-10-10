package state_machine

import (
	"testing"

	"gooncall-agent/internal/incident/model"
)

func TestCanTransition_ValidTransitions(t *testing.T) {
	cases := []struct {
		from, to model.Status
	}{
		{model.StatusOpen, model.StatusInvestigating},
		{model.StatusOpen, model.StatusFailed},
		{model.StatusInvestigating, model.StatusWaitingApproval},
		{model.StatusInvestigating, model.StatusFailed},
		{model.StatusWaitingApproval, model.StatusMitigating},
		{model.StatusWaitingApproval, model.StatusFailed},
		{model.StatusMitigating, model.StatusVerifying},
		{model.StatusMitigating, model.StatusFailed},
		{model.StatusVerifying, model.StatusResolved},
		{model.StatusVerifying, model.StatusFailed},
	}
	for _, c := range cases {
		if !CanTransition(c.from, c.to) {
			t.Errorf("CanTransition(%s, %s) = false, want true", c.from, c.to)
		}
	}
}

func TestCanTransition_ForbiddenTransitions(t *testing.T) {
	cases := []struct {
		from, to model.Status
	}{
		// 关键禁止：INVESTIGATING 直接 RESOLVED
		{model.StatusInvestigating, model.StatusResolved},
		{model.StatusOpen, model.StatusResolved},
		{model.StatusOpen, model.StatusWaitingApproval},
		{model.StatusMitigating, model.StatusResolved},
		{model.StatusWaitingApproval, model.StatusResolved},
		{model.StatusResolved, model.StatusInvestigating},
		{model.StatusFailed, model.StatusOpen},
	}
	for _, c := range cases {
		if CanTransition(c.from, c.to) {
			t.Errorf("CanTransition(%s, %s) = true, want false", c.from, c.to)
		}
	}
}

func TestTransition_ReturnsErrorOnForbidden(t *testing.T) {
	if err := Transition(model.StatusInvestigating, model.StatusResolved); err == nil {
		t.Fatal("Transition(INVESTIGATING, RESOLVED) expected error, got nil")
	}
}

func TestTransition_NoErrorOnValid(t *testing.T) {
	if err := Transition(model.StatusOpen, model.StatusInvestigating); err != nil {
		t.Fatalf("Transition(OPEN, INVESTIGATING) error = %v", err)
	}
}

func TestTransition_ErrorType(t *testing.T) {
	err := Transition(model.StatusOpen, model.StatusResolved)
	_, ok := err.(*TransitionError)
	if !ok {
		t.Fatalf("error type = %T, want *TransitionError", err)
	}
}

func TestAllowedTargets_TerminalIsEmpty(t *testing.T) {
	for _, s := range []model.Status{model.StatusResolved, model.StatusFailed, model.StatusCancelled} {
		if got := AllowedTargets(s); len(got) != 0 {
			t.Errorf("AllowedTargets(%s) = %v, want empty", s, got)
		}
	}
}

func TestAllowedTargets_Investigating(t *testing.T) {
	got := AllowedTargets(model.StatusInvestigating)
	want := []model.Status{model.StatusWaitingApproval, model.StatusFailed}
	if len(got) != len(want) {
		t.Fatalf("AllowedTargets(INVESTIGATING) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AllowedTargets(INVESTIGATING) = %v, want %v", got, want)
		}
	}
}

// TestCanTransition_DiagnosisFailureIsTerminal 覆盖诊断阶段落到终态的唯一出口：
// 重试耗尽 / 诊断 Run 失败时 Incident 必须能进入 FAILED，否则告警永久停留非终态。
func TestCanTransition_DiagnosisFailureIsTerminal(t *testing.T) {
	for _, from := range []model.Status{model.StatusOpen, model.StatusInvestigating} {
		if !CanTransition(from, model.StatusFailed) {
			t.Errorf("CanTransition(%s, FAILED) = false, want true", from)
		}
	}
}
