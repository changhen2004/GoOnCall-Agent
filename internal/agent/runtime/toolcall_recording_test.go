package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
)

// failingRunRepo 包装 MemoryRun，用于制造 ToolCall 持久化失败。
type failingRunRepo struct {
	repository.RunRepository
	failAdd    bool
	failUpdate bool
}

func (f *failingRunRepo) AddToolCall(ctx context.Context, call *incidentmodel.ToolCall) error {
	if f.failAdd {
		return errors.New("database unavailable")
	}
	return f.RunRepository.AddToolCall(ctx, call)
}

func (f *failingRunRepo) UpdateToolCall(ctx context.Context, call *incidentmodel.ToolCall) error {
	if f.failUpdate {
		return errors.New("database unavailable")
	}
	return f.RunRepository.UpdateToolCall(ctx, call)
}

// existingToolCallProbe 是探针工具：在被调用时检查自己的 ToolCall 记录是否**已经**存在，
// 模拟 worker_restart 在工具内部创建引用该 ID 的 Approval。
type existingToolCallProbe struct {
	repo        repository.RunRepository
	invoked     bool
	recordFound bool
}

type cancelAwareRunRepo struct {
	repository.RunRepository
}

func (r *cancelAwareRunRepo) UpdateToolCall(ctx context.Context, call *incidentmodel.ToolCall) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.RunRepository.UpdateToolCall(ctx, call)
}

type cancelingTool struct {
	cancel context.CancelFunc
}

func (t *cancelingTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "canceling_tool"}, nil
}

func (t *cancelingTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	t.cancel()
	return "ok", nil
}

func (p *existingToolCallProbe) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "probe_tool"}, nil
}

func (p *existingToolCallProbe) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	p.invoked = true
	callID := ToolCallIDFrom(ctx)
	if callID == "" {
		return "", errors.New("tool call id missing from context")
	}
	calls, err := p.repo.ListToolCalls(ctx, RunIDFrom(ctx))
	if err != nil {
		return "", err
	}
	for _, c := range calls {
		if c.ID == callID {
			p.recordFound = true
			break
		}
	}
	return "ok", nil
}

func newProbeRecorder(t *testing.T, repo repository.RunRepository) (*Recorder, *existingToolCallProbe) {
	t.Helper()
	inc := &incidentmodel.Incident{ID: "inc_1", Service: "svc", Title: "t", Status: incidentmodel.StatusInvestigating}
	rec := NewRecorder(repo, nil, inc, "diagnosis", 10)
	if err := rec.Start(context.Background()); err != nil {
		t.Fatalf("Recorder.Start() error = %v", err)
	}
	return rec, &existingToolCallProbe{repo: repo}
}

// TestRecordingTool_ToolCallExistsBeforeToolRuns ToolCall 记录必须先落库：
// 工具内部（审批）引用它时记录必须已经存在，否则会留下孤立 Approval。
func TestRecordingTool_ToolCallExistsBeforeToolRuns(t *testing.T) {
	mem := repository.NewMemoryRun()
	rec, probe := newProbeRecorder(t, mem)
	wrapped := &recordingTool{inner: probe, name: "probe_tool", risk: "MEDIUM", recorder: rec}

	out, err := wrapped.InvokableRun(WithRunID(context.Background(), rec.RunID()), `{"target":"w"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if out != "ok" {
		t.Fatalf("output = %q", out)
	}
	if !probe.recordFound {
		t.Fatal("ToolCall record must exist while the tool is running (approval references it)")
	}

	calls, _ := mem.ListToolCalls(context.Background(), rec.RunID())
	if len(calls) != 1 || calls[0].Status != "COMPLETED" {
		t.Fatalf("calls = %+v, want one COMPLETED record", calls)
	}
}

// TestRecordingTool_PersistFailureStopsTool 写记录失败时不得调用工具（否则会产生引用不存在记录的审批）。
func TestRecordingTool_PersistFailureStopsTool(t *testing.T) {
	repo := &failingRunRepo{RunRepository: repository.NewMemoryRun(), failAdd: true}
	rec, probe := newProbeRecorder(t, repo)
	wrapped := &recordingTool{inner: probe, name: "probe_tool", risk: "MEDIUM", recorder: rec}

	if _, err := wrapped.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("InvokableRun() must fail when the ToolCall cannot be persisted")
	}
	if probe.invoked {
		t.Fatal("the tool must NOT run when its audit record cannot be persisted")
	}
}

// TestRecordingTool_UpdateFailureIsNotSwallowed 结果落库失败必须向上传播，不能被静默吞掉。
func TestRecordingTool_UpdateFailureIsNotSwallowed(t *testing.T) {
	repo := &failingRunRepo{RunRepository: repository.NewMemoryRun(), failUpdate: true}
	rec, probe := newProbeRecorder(t, repo)
	wrapped := &recordingTool{inner: probe, name: "probe_tool", risk: "MEDIUM", recorder: rec}

	if _, err := wrapped.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("InvokableRun() must surface the ToolCall update failure")
	}
	if !probe.invoked {
		t.Fatal("the tool should already have run before the update failure")
	}
}

func TestRecordingTool_PersistsResultAfterRequestCancellation(t *testing.T) {
	mem := repository.NewMemoryRun()
	repo := &cancelAwareRunRepo{RunRepository: mem}
	rec, _ := newProbeRecorder(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := &recordingTool{
		inner:    &cancelingTool{cancel: cancel},
		name:     "canceling_tool",
		risk:     "LOW",
		recorder: rec,
	}

	if _, err := wrapped.InvokableRun(ctx, `{}`); err != nil {
		t.Fatalf("InvokableRun() error = %v, want audit result persisted after cancellation", err)
	}
	calls, _ := mem.ListToolCalls(context.Background(), rec.RunID())
	if len(calls) != 1 || calls[0].Status != "COMPLETED" {
		t.Fatalf("calls = %+v, want one COMPLETED record", calls)
	}
}
