package session_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/session"
	"gooncall-agent/internal/store"
	"gooncall-agent/internal/tools"
)

type blockingProvider struct{}

func (blockingProvider) Name() string { return "blocking" }
func (blockingProvider) Chat(context.Context, llm.Request) (*llm.Response, error) {
	return nil, errors.New("unused")
}
func (blockingProvider) ChatStream(ctx context.Context, _ llm.Request) (llm.Stream, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCancelOwnerRunsStopsActiveRuns(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	svc, err := session.New(db, session.Options{
		Models: map[string]session.Model{
			"blocking": {Provider: blockingProvider{}, ContextWindow: 8192, MaxOutputTokens: 512},
		},
		DefaultModel: "blocking",
		MaxRounds:    2,
		RunBudget:    time.Minute,
		Registry:     tools.NewRegistry(),
		ProcessID:    "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	conversation, err := svc.CreateConversation(ctx, "user-1")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	run, err := svc.Submit(ctx, "user-1", conversation.ID, "开始", "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	waitFor(t, func() bool {
		detail, err := svc.RunDetail(ctx, "user-1", run.ID)
		return err == nil && detail.Run.Status == session.RunRunning
	})

	// 其他用户的取消不影响该运行。
	if canceled, err := svc.CancelOwnerRuns(ctx, "user-2", "禁用"); err != nil || canceled != 0 {
		t.Fatalf("其他用户取消 = %d, %v", canceled, err)
	}

	canceled, err := svc.CancelOwnerRuns(ctx, "user-1", "账号已被禁用")
	if err != nil || canceled != 1 {
		t.Fatalf("CancelOwnerRuns = %d, %v", canceled, err)
	}
	waitFor(t, func() bool {
		detail, err := svc.RunDetail(ctx, "user-1", run.ID)
		return err == nil && detail.Run.Status == session.RunCanceled
	})
}
