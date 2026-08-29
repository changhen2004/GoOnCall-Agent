package incident

import (
	"context"
	"strings"
	"testing"
	"time"

	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
)

func seedRepo() repository.Repository {
	repo := repository.NewMemory()
	now := time.Now()
	_ = repo.Create(context.Background(), &model.Incident{
		ID: "inc_1", Service: "gocommunity", AlertName: "HighQueueDepth",
		Title: "rabbitmq backlog", Status: model.StatusResolved, Severity: "HIGH",
		StartedAt: now, ResolvedAt: &now, CreatedAt: now, UpdatedAt: now,
	})
	_ = repo.Create(context.Background(), &model.Incident{
		ID: "inc_2", Service: "gocommunity", AlertName: "HighErrorRate",
		Title: "error rate", Status: model.StatusOpen, Severity: "MEDIUM",
		StartedAt: now, CreatedAt: now, UpdatedAt: now,
	})
	return repo
}

func TestSearch_ByService(t *testing.T) {
	tool := New(seedRepo())
	einoTool, err := tool.EinoTool()
	if err != nil {
		t.Fatalf("EinoTool() error = %v", err)
	}

	out, err := einoTool.InvokableRun(context.Background(), `{"service":"gocommunity"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, "inc_1") || !strings.Contains(out, "inc_2") {
		t.Fatalf("expected both incidents, got: %s", out)
	}
}

func TestSearch_ByAlertName(t *testing.T) {
	tool := New(seedRepo())
	einoTool, _ := tool.EinoTool()

	out, err := einoTool.InvokableRun(context.Background(), `{"service":"gocommunity","alert_name":"HighQueueDepth"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(out, "inc_1") {
		t.Fatalf("expected inc_1, got: %s", out)
	}
	if strings.Contains(out, "inc_2") {
		t.Fatalf("unexpected inc_2 in results: %s", out)
	}
}
