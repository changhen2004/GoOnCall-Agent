package retriever

import (
	"context"
	"fmt"
	"testing"

	incidentmodel "gooncall-agent/internal/incident/model"
	kmodel "gooncall-agent/internal/knowledge/model"
	"gooncall-agent/internal/knowledge/vectorstore"
)

type fakeEmbedder struct {
	vectors map[string][]float32
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = v
		} else {
			out[i] = []float32{0, 0}
		}
	}
	return out, nil
}

// countingEmbedder 统计 Embedding 调用次数，验证增量索引跳过未变化 chunk。
type countingEmbedder struct {
	*fakeEmbedder
	calls int
}

func (c *countingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	c.calls += len(texts)
	return c.fakeEmbedder.Embed(ctx, texts)
}

func TestIndexSkipsUnchangedChunks(t *testing.T) {
	chunks := []*kmodel.Chunk{
		{ID: "a#0", Content: "aaa bbb"},
		{ID: "b#0", Content: "ccc ddd"},
	}
	embedder := &countingEmbedder{fakeEmbedder: &fakeEmbedder{vectors: map[string][]float32{
		"aaa bbb": {1, 0},
		"ccc ddd": {0, 1},
	}}}
	h := NewHybrid(chunks, embedder, vectorstore.NewMemory(), 20)

	// 首次索引：全部 embedding
	if err := h.Index(context.Background()); err != nil {
		t.Fatalf("first Index() error = %v", err)
	}
	if embedder.calls != 2 {
		t.Fatalf("first index embeds = %d, want 2", embedder.calls)
	}

	// 再次启动：内容未变化，全部跳过 embedding
	if err := h.Index(context.Background()); err != nil {
		t.Fatalf("second Index() error = %v", err)
	}
	if embedder.calls != 2 {
		t.Fatalf("second index embeds = %d, want 0 new (total 2)", embedder.calls)
	}

	// 修改一个 chunk：只重新 embedding 变化的那一个
	chunks[1].Content = "ccc ddd changed"
	if err := h.Index(context.Background()); err != nil {
		t.Fatalf("third Index() error = %v", err)
	}
	if embedder.calls != 3 {
		t.Fatalf("after change embeds = %d, want 3 (one new)", embedder.calls)
	}
}

func TestChunkHash(t *testing.T) {
	a := &kmodel.Chunk{ID: "x", Content: "hello"}
	b := &kmodel.Chunk{ID: "y", Content: "hello"}
	c := &kmodel.Chunk{ID: "x", Content: "hello!"}
	if chunkHash(a) != chunkHash(b) {
		t.Fatal("same content should hash the same regardless of id")
	}
	if chunkHash(a) == chunkHash(c) {
		t.Fatal("different content should hash differently")
	}
}

func TestHybridRetrieve(t *testing.T) {
	chunks := []*kmodel.Chunk{
		{ID: "rabbitmq.md#0", Source: "rabbitmq.md", DocType: "runbook", Title: "消费者异常", Content: "rabbitmq consumer backlog"},
		{ID: "cpu.md#0", Source: "cpu.md", DocType: "runbook", Title: "CPU 打满", Content: "cpu usage high"},
	}
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"rabbitmq consumer backlog": {1, 0},
		"cpu usage high":            {0, 1},
		"rabbitmq backlog":          {1, 0},
	}}
	store := vectorstore.NewMemory()

	h := NewHybrid(chunks, embedder, store, 20)
	if err := h.Index(context.Background()); err != nil {
		t.Fatalf("Index() error = %v", err)
	}

	results, err := h.Retrieve(context.Background(), "rabbitmq backlog", 5)
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Retrieve() returned empty")
	}
	if results[0].Chunk.ID != "rabbitmq.md#0" {
		t.Fatalf("top result = %s, want rabbitmq.md#0", results[0].Chunk.ID)
	}
}

func TestHybridRetrieve_TopK(t *testing.T) {
	chunks := []*kmodel.Chunk{
		{ID: "a#0", Content: "aaa bbb"},
		{ID: "b#0", Content: "aaa ccc"},
		{ID: "c#0", Content: "ddd eee"},
	}
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"aaa bbb": {1, 0}, "aaa ccc": {1, 0}, "ddd eee": {0, 1}, "aaa": {1, 0},
	}}
	h := NewHybrid(chunks, embedder, vectorstore.NewMemory(), 20)
	_ = h.Index(context.Background())

	results, _ := h.Retrieve(context.Background(), "aaa", 2)
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}

func TestRRFScores(t *testing.T) {
	ranks := map[string][]int{"a": {1, 3}, "b": {2, 1}}
	scores := rrfScores(ranks, 60)
	// a: 1/61 + 1/63 ≈ 0.0323; b: 1/62 + 1/61 ≈ 0.0325 → b > a
	if scores["b"] <= scores["a"] {
		t.Fatalf("b should outrank a: %v", scores)
	}
}

// recordingStore 记录每次 Search 的 topK，用于断言向量检索候选集被限制。
type recordingStore struct {
	mem   *vectorstore.Memory
	lastK int
}

func (r *recordingStore) Upsert(ctx context.Context, points []vectorstore.Point) error {
	return r.mem.Upsert(ctx, points)
}

func (r *recordingStore) Search(ctx context.Context, vector []float32, topK int) ([]vectorstore.SearchResult, error) {
	r.lastK = topK
	return r.mem.Search(ctx, vector, topK)
}

func (r *recordingStore) Hashes(ctx context.Context) (map[string]string, error) {
	return r.mem.Hashes(ctx)
}

func TestVectorSearchCandidateK(t *testing.T) {
	chunks := make([]*kmodel.Chunk, 0, 20)
	vectors := make(map[string][]float32, 20)
	for i := 0; i < 20; i++ {
		content := fmt.Sprintf("content %d", i)
		chunks = append(chunks, &kmodel.Chunk{ID: fmt.Sprintf("c%d", i), Content: content})
		vectors[content] = []float32{float32(i), 0}
	}
	embedder := &fakeEmbedder{vectors: vectors}

	// candidateK=5，知识库 20 个 chunk：向量检索 topK 必须是 5，而不是 len(chunks)=20。
	store := &recordingStore{mem: vectorstore.NewMemory()}
	h := NewHybrid(chunks, embedder, store, 5)
	if err := h.Index(context.Background()); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if _, err := h.Retrieve(context.Background(), "content 19", 3); err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if store.lastK != 5 {
		t.Fatalf("vector Search topK = %d, want candidateK 5 (not len(chunks)=20)", store.lastK)
	}

	// 请求 topK 超过 candidateK 时，候选集至少覆盖请求条数。
	store.lastK = 0
	if _, err := h.Retrieve(context.Background(), "content 19", 8); err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}
	if store.lastK != 8 {
		t.Fatalf("vector Search topK = %d, want max(candidateK, topK)=8", store.lastK)
	}
}

func TestResultToEvidence(t *testing.T) {
	r := Result{
		Chunk: &kmodel.Chunk{Source: "rabbitmq.md", DocType: "runbook", Title: "消费者异常", Content: "内容"},
		Score: 0.9,
	}
	ev := r.ToEvidence("run_1")
	if ev.Type != incidentmodel.EvidenceRunbook {
		t.Fatalf("type = %s", ev.Type)
	}
	if ev.RunID != "run_1" || ev.Source != "rabbitmq.md" {
		t.Fatalf("evidence = %+v", ev)
	}
	if ev.ID == "" {
		t.Fatal("evidence id should not be empty")
	}
}
