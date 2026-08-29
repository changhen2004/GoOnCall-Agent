package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"gooncall-agent/internal/incident/model"
)

func newTestIncident(id, service, status string) *model.Incident {
	now := time.Now()
	return &model.Incident{
		ID:        id,
		Service:   service,
		Status:    model.Status(status),
		Title:     "test incident",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestMemory_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	inc := newTestIncident("inc_1", "svc-a", "OPEN")

	if err := repo.Create(ctx, inc); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.GetByID(ctx, "inc_1")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.ID != "inc_1" || got.Service != "svc-a" {
		t.Fatalf("GetByID() = %+v", got)
	}
}

func TestMemory_GetByIDNotFound(t *testing.T) {
	repo := NewMemory()
	if _, err := repo.GetByID(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByID() error = %v, want ErrNotFound", err)
	}
}

func TestMemory_GetByFingerprint(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	inc := newTestIncident("inc_1", "svc-a", "OPEN")
	inc.Fingerprint = "fp_1"
	if err := repo.Create(ctx, inc); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := repo.GetByFingerprint(ctx, "fp_1")
	if err != nil {
		t.Fatalf("GetByFingerprint() error = %v", err)
	}
	if got.ID != "inc_1" {
		t.Fatalf("GetByFingerprint() = %+v", got)
	}

	if _, err := repo.GetByFingerprint(ctx, "fp_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByFingerprint(missing) error = %v, want ErrNotFound", err)
	}
}

func TestMemory_CreateConflict(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	if err := repo.Create(ctx, newTestIncident("inc_1", "svc-a", "OPEN")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := repo.Create(ctx, newTestIncident("inc_1", "svc-a", "OPEN")); !errors.Is(err, ErrConflict) {
		t.Fatalf("Create(dup) error = %v, want ErrConflict", err)
	}
}

func TestMemory_ListFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	for i, id := range []string{"inc_1", "inc_2", "inc_3"} {
		inc := newTestIncident(id, "svc-a", "OPEN")
		inc.CreatedAt = inc.CreatedAt.Add(time.Duration(i) * time.Second)
		if err := repo.Create(ctx, inc); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	if err := repo.Create(ctx, newTestIncident("inc_4", "svc-b", "RESOLVED")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	all, err := repo.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List() len = %d, want 4", len(all))
	}

	svcA, _ := repo.List(ctx, ListFilter{Service: "svc-a"})
	if len(svcA) != 3 {
		t.Fatalf("List(service=svc-a) len = %d, want 3", len(svcA))
	}

	resolved, _ := repo.List(ctx, ListFilter{Status: model.StatusResolved})
	if len(resolved) != 1 || resolved[0].ID != "inc_4" {
		t.Fatalf("List(status=RESOLVED) = %+v", resolved)
	}

	limited, _ := repo.List(ctx, ListFilter{Limit: 2})
	if len(limited) != 2 {
		t.Fatalf("List(limit=2) len = %d, want 2", len(limited))
	}

	offset, _ := repo.List(ctx, ListFilter{Offset: 3})
	if len(offset) != 1 {
		t.Fatalf("List(offset=3) len = %d, want 1", len(offset))
	}
}

func TestMemory_Update(t *testing.T) {
	ctx := context.Background()
	repo := NewMemory()
	if err := repo.Create(ctx, newTestIncident("inc_1", "svc-a", "OPEN")); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	inc, _ := repo.GetByID(ctx, "inc_1")
	inc.Status = model.StatusInvestigating
	if err := repo.Update(ctx, inc); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, _ := repo.GetByID(ctx, "inc_1")
	if got.Status != model.StatusInvestigating {
		t.Fatalf("status = %s, want INVESTIGATING", got.Status)
	}
}

func TestMemory_UpdateNotFound(t *testing.T) {
	repo := NewMemory()
	if err := repo.Update(context.Background(), newTestIncident("missing", "svc-a", "OPEN")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v, want ErrNotFound", err)
	}
}
