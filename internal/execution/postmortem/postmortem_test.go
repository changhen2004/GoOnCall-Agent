package postmortem

import (
	"strings"
	"testing"

	"gooncall-agent/internal/incident/model"
)

func TestGenerate(t *testing.T) {
	g := New()
	inc := &model.Incident{
		ID:          "inc_1",
		Title:       "rabbitmq backlog",
		Description: "queue depth 从 120 上升到 2418",
	}
	out := g.Generate(inc, "worker 连接异常", []string{"consumers 8 -> 2", "queue depth 120 -> 2418"}, "restart worker")

	for _, want := range []string{
		"# Incident inc_1",
		"## Summary",
		"rabbitmq backlog",
		"## Root Cause",
		"worker 连接异常",
		"## Evidence",
		"- consumers 8 -> 2",
		"## Resolution",
		"restart worker",
		"## Prevention",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("postmortem missing %q:\n%s", want, out)
		}
	}
}

func TestGenerate_EmptyEvidence(t *testing.T) {
	g := New()
	out := g.Generate(&model.Incident{ID: "inc_1", Title: "t"}, "root", nil, "res")
	if !strings.Contains(out, "- 无") {
		t.Fatalf("expected empty evidence placeholder, got:\n%s", out)
	}
}
