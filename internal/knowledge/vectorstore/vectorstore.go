// Package vectorstore 提供向量存储抽象及实现。
package vectorstore

import (
	"context"
	"math"
	"sort"
	"sync"
)

// Point 是一个待存储的向量点。
type Point struct {
	ID      string
	Vector  []float32
	Payload map[string]any
}

// SearchResult 是向量搜索结果。
type SearchResult struct {
	ID      string
	Score   float32
	Payload map[string]any
}

// VectorStore 是向量存储抽象。
type VectorStore interface {
	Upsert(ctx context.Context, points []Point) error
	Search(ctx context.Context, vector []float32, topK int) ([]SearchResult, error)
}

// Memory 是内存向量存储（余弦相似度），用于单元测试与本地开发。
type Memory struct {
	mu     sync.RWMutex
	points map[string]Point
}

// NewMemory 创建内存向量存储。
func NewMemory() *Memory {
	return &Memory{points: make(map[string]Point)}
}

func (m *Memory) Upsert(_ context.Context, points []Point) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range points {
		m.points[p.ID] = p
	}
	return nil
}

func (m *Memory) Search(_ context.Context, vector []float32, topK int) ([]SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	type scored struct {
		id    string
		score float32
	}
	scoredPoints := make([]scored, 0, len(m.points))
	for id, p := range m.points {
		scoredPoints = append(scoredPoints, scored{id: id, score: cosine(vector, p.Vector)})
	}
	sort.Slice(scoredPoints, func(i, j int) bool { return scoredPoints[i].score > scoredPoints[j].score })
	if topK > 0 && topK < len(scoredPoints) {
		scoredPoints = scoredPoints[:topK]
	}

	out := make([]SearchResult, 0, len(scoredPoints))
	for _, sp := range scoredPoints {
		out = append(out, SearchResult{ID: sp.id, Score: sp.score, Payload: m.points[sp.id].Payload})
	}
	return out, nil
}

func cosine(a, b []float32) float32 {
	var dot, na, nb float32
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(na))) * float32(math.Sqrt(float64(nb))))
}
