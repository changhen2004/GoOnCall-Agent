package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
)

func newService() *Service {
	return New(repository.NewMemory())
}

func TestCreate_DefaultsAndFingerprint(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	inc, created, err := svc.Create(ctx, CreateIncidentInput{
		Service:   "gocommunity",
		Title:     "rabbitmq backlog",
		AlertName: "HighQueueDepth",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !created {
		t.Fatal("Create() created = false, want true")
	}
	if inc.Status != model.StatusOpen {
		t.Errorf("status = %s, want OPEN", inc.Status)
	}
	if inc.Severity != string(model.SeverityMedium) {
		t.Errorf("severity = %s, want MEDIUM", inc.Severity)
	}
	if inc.Fingerprint == "" {
		t.Error("fingerprint should not be empty")
	}
	if inc.ID == "" {
		t.Error("id should not be empty")
	}
	if inc.CreatedAt.IsZero() {
		t.Error("created_at should be set")
	}
}

func TestCreate_DedupByAlertName(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	first, created, err := svc.Create(ctx, CreateIncidentInput{
		Service: "gocommunity", Title: "first title", AlertName: "HighErrorRate",
	})
	if err != nil || !created {
		t.Fatalf("first Create() = (%v, %v), err=%v", first.ID, created, err)
	}

	second, created2, err := svc.Create(ctx, CreateIncidentInput{
		Service: "gocommunity", Title: "different title", AlertName: "HighErrorRate",
	})
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if created2 {
		t.Fatal("second Create() created = true, want false (dedup)")
	}
	if second.ID != first.ID {
		t.Errorf("dedup returned %s, want %s", second.ID, first.ID)
	}
}

func TestCreate_Validation(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	if _, _, err := svc.Create(ctx, CreateIncidentInput{Service: "", Title: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create(empty service) error = %v, want ErrInvalidInput", err)
	}
	if _, _, err := svc.Create(ctx, CreateIncidentInput{Service: "svc", Title: ""}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Create(empty title) error = %v, want ErrInvalidInput", err)
	}
}

// TestCreate_NewIncidentAfterTerminal 验证告警复发（前一轮已终态）创建的是新 Incident：
// ID 不同、旧记录保持终态不被重开。
func TestCreate_NewIncidentAfterTerminal(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	first, created, err := svc.Create(ctx, CreateIncidentInput{
		Service: "gocommunity", AlertName: "HighErrorRate", Title: "err",
	})
	if err != nil || !created {
		t.Fatalf("first Create() = (%v, %v), err=%v", first.ID, created, err)
	}
	// 外部恢复：Alertmanager resolved 通知直接关闭 Incident。
	resolvedInc, err := svc.ResolveExternally(ctx, first.Fingerprint)
	if err != nil {
		t.Fatalf("ResolveExternally() error = %v", err)
	}
	if resolvedInc.Status != model.StatusResolved {
		t.Fatalf("resolved status = %s, want RESOLVED", resolvedInc.Status)
	}

	// 告警复发：同一指纹再次 firing，必须开新 Incident（新告警周期）。
	second, created2, err := svc.Create(ctx, CreateIncidentInput{
		Service: "gocommunity", AlertName: "HighErrorRate", Title: "err again",
	})
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if !created2 {
		t.Fatal("second Create() created = false, want true (new cycle)")
	}
	if second.ID == first.ID {
		t.Errorf("recurrence returned same incident %s, want a new ID", first.ID)
	}
	if second.Status != model.StatusOpen {
		t.Errorf("new incident status = %s, want OPEN", second.Status)
	}
	if second.ResolvedAt != nil {
		t.Error("new incident resolved_at should be nil")
	}

	// 旧记录未被改动：仍是终态，版本号未变。
	old, err := svc.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get(first) error = %v", err)
	}
	if old.Status != model.StatusResolved || old.ResolvedAt == nil {
		t.Errorf("old incident = %s resolved_at=%v, want RESOLVED with timestamp", old.Status, old.ResolvedAt)
	}
	if old.Version != resolvedInc.Version {
		t.Errorf("old incident version = %d, want unchanged %d", old.Version, resolvedInc.Version)
	}

	// 同一周期内再次 firing 仍然去重到新记录。
	third, created3, err := svc.Create(ctx, CreateIncidentInput{
		Service: "gocommunity", AlertName: "HighErrorRate", Title: "err again",
	})
	if err != nil || created3 {
		t.Fatalf("third Create() = (%v, created=%v), want dedup on the new cycle", third, created3)
	}
	if third.ID != second.ID {
		t.Errorf("dedup returned %s, want %s", third.ID, second.ID)
	}
}

func TestResolveExternally_IdempotentWithoutActiveIncident(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	// 完全没有该指纹的记录：无从恢复，返回 ErrNotFound（不伪造记录）。
	if inc, err := svc.ResolveExternally(ctx, "fp-unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ResolveExternally(unknown) = (%+v, %v), want ErrNotFound", inc, err)
	}

	// 已终态：幂等成功，返回最近一条历史记录且不修改它。
	first, _, err := svc.Create(ctx, CreateIncidentInput{Service: "svc", AlertName: "a", Title: "t"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	resolved, err := svc.ResolveExternally(ctx, first.Fingerprint)
	if err != nil {
		t.Fatalf("ResolveExternally() error = %v", err)
	}
	again, err := svc.ResolveExternally(ctx, first.Fingerprint)
	if err != nil {
		t.Fatalf("ResolveExternally(terminal) error = %v, want nil", err)
	}
	if again == nil || again.ID != first.ID {
		t.Fatalf("ResolveExternally(terminal) = %+v, want the historical record %s", again, first.ID)
	}
	if again.Status != model.StatusResolved {
		t.Errorf("returned status = %s, want RESOLVED", again.Status)
	}
	old, err := svc.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if old.Version != resolved.Version {
		t.Errorf("terminal incident version changed: %d -> %d", resolved.Version, old.Version)
	}
}

// TestResolveExternally_DoesNotCloseExecutionInProgress 处置已取得执行权后，resolved webhook
// 不能抢先关单；Alertmanager 可在处置结束后重试。
func TestResolveExternally_DoesNotCloseExecutionInProgress(t *testing.T) {
	for _, status := range []model.Status{model.StatusMitigating, model.StatusVerifying} {
		t.Run(string(status), func(t *testing.T) {
			repo := repository.NewMemory()
			svc := New(repo)
			inc, _, err := svc.Create(context.Background(), CreateIncidentInput{Service: "svc", Title: string(status)})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			for _, next := range []model.Status{model.StatusInvestigating, model.StatusWaitingApproval, model.StatusMitigating, model.StatusVerifying} {
				if next == model.StatusVerifying && status == model.StatusMitigating {
					break
				}
				if _, err := svc.MoveTo(context.Background(), inc.ID, next); err != nil {
					t.Fatalf("MoveTo(%s) error = %v", next, err)
				}
				if next == status {
					break
				}
			}

			if _, err := svc.ResolveExternally(context.Background(), inc.Fingerprint); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("ResolveExternally() error = %v, want ErrInvalidTransition", err)
			}
			got, _ := svc.Get(context.Background(), inc.ID)
			if got.Status != status {
				t.Fatalf("status = %s, want %s", got.Status, status)
			}
		})
	}
}

func TestTransition_WritesEvents(t *testing.T) {
	ctx := context.Background()
	svc := New(repository.NewMemory())

	inc, _, err := svc.Create(ctx, CreateIncidentInput{Service: "svc", AlertName: "a", Title: "t"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.Analyze(ctx, inc.ID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	// 非法迁移：不写事件、必须报错。
	if _, err := svc.Resolve(ctx, inc.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Resolve(INVESTIGATING) error = %v, want ErrInvalidTransition", err)
	}
	if _, err := svc.Fail(ctx, inc.ID, "diagnosis failed", "llm timeout"); err != nil {
		t.Fatalf("Fail() error = %v", err)
	}

	got, err := svc.ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	// 创建 + OPEN->INVESTIGATING + INVESTIGATING->FAILED，非法迁移不计入。
	if len(got) != 3 {
		t.Fatalf("events = %d, want 3", len(got))
	}
	type want struct {
		eventType  string
		from, to   model.Status
		reasonText string
	}
	wants := []want{
		{model.EventIncidentCreated, "", model.StatusOpen, "alert firing"},
		{model.EventStatusTransition, model.StatusOpen, model.StatusInvestigating, "state machine transition"},
		{model.EventStatusTransition, model.StatusInvestigating, model.StatusFailed, "diagnosis failed"},
	}
	for i, w := range wants {
		if got[i].EventType != w.eventType {
			t.Errorf("event[%d].type = %s, want %s", i, got[i].EventType, w.eventType)
		}
		from, to := got[i].StatusChange()
		if from != w.from || to != w.to {
			t.Errorf("event[%d] = %s -> %s, want %s -> %s", i, from, to, w.from, w.to)
		}
		if got[i].ReasonText() != w.reasonText {
			t.Errorf("event[%d].reason = %q, want %q", i, got[i].ReasonText(), w.reasonText)
		}
		if got[i].CreatedAt.IsZero() {
			t.Errorf("event[%d].created_at is zero", i)
		}
	}
	// 终态事件必须带上错误原因。
	if got[2].Payload["error"] != "llm timeout" {
		t.Errorf("event[2].error = %v, want llm timeout", got[2].Payload["error"])
	}
}

// failingEventRepo 让"事件写入"失败：CreateWithEvent/UpdateWithEvent 不落库并返回错误，
// 其余读写委托给 Memory。用于确认 Service 始终走原子写路径，不会在事件失败时留下半截状态。
type failingEventRepo struct {
	*repository.Memory
	atomicCalls int
}

func (f *failingEventRepo) CreateWithEvent(ctx context.Context, inc *model.Incident, ev *model.IncidentEvent) error {
	f.atomicCalls++
	if ev == nil {
		return errors.New("event must be written atomically with the incident")
	}
	return errors.New("event write failed")
}

func (f *failingEventRepo) UpdateWithEvent(ctx context.Context, inc *model.Incident, ev *model.IncidentEvent) error {
	f.atomicCalls++
	if ev == nil {
		return errors.New("event must be written atomically with the incident")
	}
	return errors.New("event write failed")
}

// TestService_EventWriteFailureLeavesNoPartialState 覆盖"事件写失败必须整体回滚"：
// 接口返回错误，且 Incident 不能被创建、状态不能被改动。
func TestService_EventWriteFailureLeavesNoPartialState(t *testing.T) {
	ctx := context.Background()
	repo := &failingEventRepo{Memory: repository.NewMemory()}
	svc := New(repo)

	if _, _, err := svc.Create(ctx, CreateIncidentInput{Service: "svc", AlertName: "a", Title: "t"}); err == nil {
		t.Fatal("Create() error = nil, want event write error")
	}
	if repo.atomicCalls == 0 {
		t.Fatal("Create() 没有走原子写路径（CreateWithEvent）")
	}
	list, err := svc.List(ctx, repository.ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("incidents = %d, want 0（事件写失败不能留下 Incident）", len(list))
	}

	// 正常创建一条，再让迁移时的事件写入失败：状态与 version 必须不变。
	ok := repository.NewMemory()
	inc, _, err := New(ok).Create(ctx, CreateIncidentInput{Service: "svc", AlertName: "b", Title: "t"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	before, err := ok.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	failing := &failingEventRepo{Memory: ok}
	if _, err := New(failing).Analyze(ctx, inc.ID); err == nil {
		t.Fatal("Analyze() error = nil, want event write error")
	}
	if failing.atomicCalls == 0 {
		t.Fatal("Analyze() 没有走原子写路径（UpdateWithEvent）")
	}
	after, err := ok.GetByID(ctx, inc.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if after.Status != before.Status || after.Version != before.Version {
		t.Fatalf("status after failed transition = %s v%d, want unchanged %s v%d", after.Status, after.Version, before.Status, before.Version)
	}
}

func TestResolveExternally_WritesEvent(t *testing.T) {
	ctx := context.Background()
	svc := New(repository.NewMemory())

	inc, _, err := svc.Create(ctx, CreateIncidentInput{Service: "svc", AlertName: "a", Title: "t"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := svc.Analyze(ctx, inc.ID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if _, err := svc.ResolveExternally(ctx, inc.Fingerprint); err != nil {
		t.Fatalf("ResolveExternally() error = %v", err)
	}

	got, err := svc.ListEvents(ctx, inc.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("events = %d, want 3", len(got))
	}
	from, to := got[2].StatusChange()
	if from != model.StatusInvestigating || to != model.StatusResolved {
		t.Errorf("last event = %s -> %s, want INVESTIGATING -> RESOLVED", from, to)
	}
	if got[2].ReasonText() != "external alert resolved" {
		t.Errorf("reason = %q", got[2].ReasonText())
	}
}

// TestCreate_ConcurrentDedup 验证高并发创建同一指纹 Incident 时只落库一条：
// 一个请求 created=true，其余命中去重（检查命中或唯一约束冲突兜底）返回同一记录。
func TestCreate_ConcurrentDedup(t *testing.T) {
	svc := newService()
	start := make(chan struct{})
	type result struct {
		inc     *model.Incident
		created bool
		err     error
	}
	const workers = 20
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			inc, created, err := svc.Create(context.Background(), CreateIncidentInput{
				Service: "svc-a", AlertName: "alert-x", Title: "same title",
			})
			results <- result{inc: inc, created: created, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var createdCount int
	var seenID string
	for r := range results {
		if r.err != nil {
			t.Fatalf("Create() error = %v", r.err)
		}
		if r.inc == nil {
			t.Fatal("Create() returned nil incident")
		}
		if seenID == "" {
			seenID = r.inc.ID
		}
		if r.inc.ID != seenID {
			t.Fatalf("dedup returned different ids: %s vs %s", r.inc.ID, seenID)
		}
		if r.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created = %d, want 1", createdCount)
	}

	list, err := svc.List(context.Background(), repository.ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("incidents stored = %d, want 1 (dedup)", len(list))
	}
}

func TestCreate_DedupActiveIncident(t *testing.T) {
	ctx := context.Background()
	svc := newService()

	first, created, err := svc.Create(ctx, CreateIncidentInput{Service: "svc", AlertName: "a", Title: "t"})
	if err != nil || !created {
		t.Fatalf("first Create() = (%v, %v), err=%v", first.ID, created, err)
	}

	// 活跃周期内重复 firing：直接去重返回，不重开、不改变状态。
	second, created2, err := svc.Create(ctx, CreateIncidentInput{Service: "svc", AlertName: "a", Title: "t2"})
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	if created2 {
		t.Fatal("second Create() created = true, want false (active dedup)")
	}
	if second.ID != first.ID {
		t.Errorf("dedup returned %s, want %s", second.ID, first.ID)
	}
	if second.Status != model.StatusOpen {
		t.Errorf("dedup status = %s, want unchanged OPEN", second.Status)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newService()
	if _, err := svc.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

func TestAnalyze_TransitionsToInvestigating(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	inc, _, _ := svc.Create(ctx, CreateIncidentInput{Service: "svc", Title: "t"})

	got, err := svc.Analyze(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if got.Status != model.StatusInvestigating {
		t.Fatalf("status = %s, want INVESTIGATING", got.Status)
	}
}

func TestResolve_SetsResolvedAt(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	inc, _, _ := svc.Create(ctx, CreateIncidentInput{Service: "svc", Title: "t"})

	if _, err := svc.Analyze(ctx, inc.ID); err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	// 严格状态机：INVESTIGATING -> WAITING_APPROVAL -> MITIGATING -> VERIFYING -> RESOLVED
	for _, next := range []model.Status{model.StatusWaitingApproval, model.StatusMitigating, model.StatusVerifying} {
		if _, err := svc.MoveTo(ctx, inc.ID, next); err != nil {
			t.Fatalf("MoveTo(%s) error = %v", next, err)
		}
	}

	got, err := svc.Resolve(ctx, inc.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got.Status != model.StatusResolved {
		t.Fatalf("status = %s, want RESOLVED", got.Status)
	}
	if got.ResolvedAt == nil {
		t.Fatal("resolved_at should be set")
	}
}

func TestResolve_FromOpenIsInvalid(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	inc, _, _ := svc.Create(ctx, CreateIncidentInput{Service: "svc", Title: "t"})

	if _, err := svc.Resolve(ctx, inc.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Resolve(OPEN) error = %v, want ErrInvalidTransition", err)
	}
}

func TestList_DelegatesToRepository(t *testing.T) {
	ctx := context.Background()
	svc := newService()
	for i, s := range []string{"svc-a", "svc-b"} {
		if _, _, err := svc.Create(ctx, CreateIncidentInput{Service: s, Title: "t"}); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		_ = i
	}

	list, err := svc.List(ctx, repository.ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}
}

// barrierRepo 在 GetByID 处设置屏障：两个并发调用都读取到同一版本快照后再放行，
// 从而确定性触发乐观锁冲突（先读后写窗口被强制拉开）。
type barrierRepo struct {
	repository.Repository
	mu      sync.Mutex
	hits    int
	barrier chan struct{}
}

func (b *barrierRepo) GetByID(ctx context.Context, id string) (*model.Incident, error) {
	inc, err := b.Repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.hits++
	if b.hits == 2 {
		close(b.barrier)
		b.barrier = nil
	}
	wait := b.barrier
	b.mu.Unlock()
	if wait != nil {
		<-wait
	}
	return inc, nil
}

func TestTransition_ConcurrentModification(t *testing.T) {
	br := &barrierRepo{Repository: repository.NewMemory(), barrier: make(chan struct{})}
	svc := New(br)

	inc, created, err := svc.Create(context.Background(), CreateIncidentInput{Service: "svc-a", Title: "t"})
	if err != nil || !created {
		t.Fatalf("Create() = %v, %v", inc, err)
	}

	// 两个请求同时把 OPEN -> INVESTIGATING：一个成功，另一个 version CAS 冲突。
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.Transition(context.Background(), inc.ID, model.StatusInvestigating)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var okCount, conflictCount int
	for err := range errs {
		switch {
		case err == nil:
			okCount++
		case errors.Is(err, ErrConcurrentModification):
			conflictCount++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("ok = %d, conflict = %d, want 1/1", okCount, conflictCount)
	}

	got, err := svc.Get(context.Background(), inc.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Status != model.StatusInvestigating || got.Version != 1 {
		t.Fatalf("final incident = %s v%d, want INVESTIGATING v1", got.Status, got.Version)
	}
}
