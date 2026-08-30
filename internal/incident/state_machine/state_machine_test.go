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
		{model.StatusInvestigating, model.StatusWaitingApproval},
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
	if len(got) != 1 || got[0] != model.StatusWaitingApproval {
		t.Fatalf("AllowedTargets(INVESTIGATING) = %v, want [WAITING_APPROVAL]", got)
	}
}
