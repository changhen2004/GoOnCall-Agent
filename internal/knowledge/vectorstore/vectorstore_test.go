package vectorstore

import (
	"context"
	"testing"
)

func TestMemory_UpsertAndSearch(t *testing.T) {
	store := NewMemory()
	_ = store.Upsert(context.Background(), []Point{
		{ID: "a", Vector: []float32{1, 0, 0}},
		{ID: "b", Vector: []float32{0, 1, 0}},
		{ID: "c", Vector: []float32{0.9, 0.1, 0}},
	})

	res, err := store.Search(context.Background(), []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2", len(res))
	}
	if res[0].ID != "a" {
		t.Fatalf("top result = %s, want a", res[0].ID)
	}
	if res[1].ID != "c" {
		t.Fatalf("second result = %s, want c", res[1].ID)
	}
	if res[0].Score <= res[1].Score {
		t.Fatalf("scores not sorted: %v", res)
	}
}

func TestMemory_SearchTopKAll(t *testing.T) {
	store := NewMemory()
	_ = store.Upsert(context.Background(), []Point{
		{ID: "a", Vector: []float32{1, 0}},
		{ID: "b", Vector: []float32{0, 1}},
	})

	res, _ := store.Search(context.Background(), []float32{1, 0}, 10)
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2", len(res))
	}
}

func TestMemory_Hashes(t *testing.T) {
	store := NewMemory()
	_ = store.Upsert(context.Background(), []Point{
		{ID: "a", Vector: []float32{1, 0}, Payload: map[string]any{"chunk_hash": "h1"}},
		{ID: "b", Vector: []float32{0, 1}, Payload: map[string]any{"chunk_hash": "h2"}},
		{ID: "c", Vector: []float32{1, 1}}, // 无 chunk_hash，应被忽略
	})

	got, err := store.Hashes(context.Background())
	if err != nil {
		t.Fatalf("Hashes() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Hashes() len = %d, want 2", len(got))
	}
	if got["a"] != "h1" || got["b"] != "h2" {
		t.Fatalf("Hashes() = %v", got)
	}
	if _, ok := got["c"]; ok {
		t.Fatal("Hashes() should omit points without chunk_hash")
	}
}
