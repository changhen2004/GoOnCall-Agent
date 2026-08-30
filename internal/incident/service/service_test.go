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
