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
		{model.StatusInvestigating, model.StatusNeedApproval},
		{model.StatusInvestigating, model.StatusResolved},
		{model.StatusNeedApproval, model.StatusWaitingApproval},
		{model.StatusWaitingApproval, model.StatusMitigating},
		{model.StatusWaitingApproval, model.StatusFailed},
		{model.StatusMitigating, model.StatusVerifying},
		{model.StatusVerifying, model.StatusResolved},
		{model.StatusVerifying, model.StatusInvestigating},
		{model.StatusOpen, model.StatusCancelled},
	}
	for _, c := range cases {
		if !CanTransition(c.from, c.to) {
			t.Errorf("CanTransition(%s, %s) = false, want true", c.from, c.to)
		}
	}
}

func TestCanTransition_InvalidTransitions(t *testing.T) {
	cases := []struct {
		from, to model.Status
	}{
		{model.StatusOpen, model.StatusResolved},          // 跳过调查直接关闭
		{model.StatusResolved, model.StatusInvestigating}, // 终态不可回退
		{model.StatusFailed, model.StatusOpen},
		{model.StatusCancelled, model.StatusInvestigating},
		{model.StatusMitigating, model.StatusOpen}, // 处置中不可回到 OPEN
		{model.StatusWaitingApproval, model.StatusOpen},
	}
	for _, c := range cases {
		if CanTransition(c.from, c.to) {
			t.Errorf("CanTransition(%s, %s) = true, want false", c.from, c.to)
		}
	}
}

func TestTransition_ReturnsErrorOnInvalid(t *testing.T) {
	if err := Transition(model.StatusOpen, model.StatusResolved); err == nil {
		t.Fatal("Transition(OPEN, RESOLVED) expected error, got nil")
	}
}

func TestTransition_NoErrorOnValid(t *testing.T) {
	if err := Transition(model.StatusOpen, model.StatusInvestigating); err != nil {
		t.Fatalf("Transition(OPEN, INVESTIGATING) error = %v", err)
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
	if len(got) == 0 {
		t.Fatal("AllowedTargets(INVESTIGATING) should not be empty")
	}
	want := map[model.Status]bool{
		model.StatusNeedApproval: true,
		model.StatusResolved:     true,
		model.StatusFailed:       true,
		model.StatusCancelled:    true,
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected target %s", s)
		}
		delete(want, s)
	}
	if len(want) != 0 {
		t.Errorf("missing targets: %v", want)
	}
}
