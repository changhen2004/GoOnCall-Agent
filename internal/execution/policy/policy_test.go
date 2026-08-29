package policy

import (
	"testing"

	"gooncall-agent/internal/tool/registry"
)

func TestEvaluate_LowAllowed(t *testing.T) {
	e := New(true)
	if got := e.Evaluate(registry.RiskLow); got != DecisionAllow {
		t.Fatalf("Evaluate(LOW) = %s, want ALLOW", got)
	}
}

func TestEvaluate_MediumRequiresApproval(t *testing.T) {
	e := New(true)
	if got := e.Evaluate(registry.RiskMedium); got != DecisionRequireApproval {
		t.Fatalf("Evaluate(MEDIUM) = %s, want REQUIRE_APPROVAL", got)
	}
	if got := e.Evaluate(registry.RiskHigh); got != DecisionRequireApproval {
		t.Fatalf("Evaluate(HIGH) = %s, want REQUIRE_APPROVAL", got)
	}
}

func TestEvaluate_CriticalDenied(t *testing.T) {
	e := New(true)
	if got := e.Evaluate(registry.RiskCritical); got != DecisionDeny {
		t.Fatalf("Evaluate(CRITICAL) = %s, want DENY", got)
	}
}

func TestEvaluate_MediumDeniedWhenApprovalDisabled(t *testing.T) {
	e := New(false)
	if got := e.Evaluate(registry.RiskMedium); got != DecisionDeny {
		t.Fatalf("Evaluate(MEDIUM, disabled) = %s, want DENY", got)
	}
}

func TestEvaluate_UnknownDenied(t *testing.T) {
	e := New(true)
	if got := e.Evaluate(registry.RiskLevel("UNKNOWN")); got != DecisionDeny {
		t.Fatalf("Evaluate(UNKNOWN) = %s, want DENY", got)
	}
}
