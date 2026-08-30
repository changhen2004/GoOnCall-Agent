// Package retriever 提供混合检索：词法 + 向量，RRF 融合。
package retriever

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	incidentmodel "gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/knowledge/embedding"
	kmodel "gooncall-agent/internal/knowledge/model"
	"gooncall-agent/internal/knowledge/vectorstore"
)

// Result 是单条检索结果。
type Result struct {
	Chunk *kmodel.Chunk
	Score float64
}

// Retriever 是检索抽象。
type Retriever interface {
	Retrieve(ctx context.Context, query string, topK int) ([]Result, error)
}

// Hybrid 是混合检索器（词法 + 向量，RRF 融合）。
type Hybrid struct {
	chunks     []*kmodel.Chunk
	embedder   embedding.Embedder
	vector     vectorstore.VectorStore
	rrfK       int
	candidateK int // 向量检索候选集大小（远小于全量 chunks）
}

// NewHybrid 创建混合检索器。candidateK 为向量检索候选集大小，
// 应显著小于 chunks 总量（默认 20），避免每次查询按全量 chunks 做向量 topK。
func NewHybrid(chunks []*kmodel.Chunk, embedder embedding.Embedder, vector vectorstore.VectorStore, candidateK int) *Hybrid {
	if candidateK <= 0 {
		candidateK = 20
	}
	return &Hybrid{chunks: chunks, embedder: embedder, vector: vector, rrfK: 60, candidateK: candidateK}
}

// Index 向量化所有 chunks 并写入向量存储。
func (h *Hybrid) Index(ctx context.Context) error {
	for _, c := range h.chunks {
		vecs, err := h.embedder.Embed(ctx, []string{c.Content})
		if err != nil {
			return err
		}
		if len(vecs) != 1 {
			continue
		}
		_ = h.vector.Upsert(ctx, []vectorstore.Point{{
			ID:      c.ID,
			Vector:  vecs[0],
			Payload: map[string]any{"source": c.Source, "doc_type": c.DocType},
		}})
	}
	return nil
}

// Retrieve 执行混合检索，返回 topK 条结果（按融合分数降序）。
func (h *Hybrid) Retrieve(ctx context.Context, query string, topK int) ([]Result, error) {
	if topK <= 0 {
		topK = 5
	}

	ranks := make(map[string][]int)
	for id, r := range lexicalRanks(query, h.chunks) {
		ranks[id] = append(ranks[id], r)
	}
	// 向量检索候选集至少覆盖请求的 topK，避免 RRF 后结果不足。
	k := h.candidateK
	if k < topK {
		k = topK
	}
	vr, err := h.vectorRanks(ctx, query, k)
	if err != nil {
		return nil, err
	}
	for id, r := range vr {
		ranks[id] = append(ranks[id], r)
	}

	scores := rrfScores(ranks, h.rrfK)

	chunkMap := make(map[string]*kmodel.Chunk, len(h.chunks))
	for _, c := range h.chunks {
		chunkMap[c.ID] = c
	}

	type scored struct {
		chunk *kmodel.Chunk
		score float64
	}
	list := make([]scored, 0, len(scores))
	for id, s := range scores {
		if c, ok := chunkMap[id]; ok {
			list = append(list, scored{chunk: c, score: s})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
	if topK < len(list) {
		list = list[:topK]
	}

	out := make([]Result, 0, len(list))
	for _, sc := range list {
		out = append(out, Result{Chunk: sc.chunk, Score: sc.score})
	}
	return out, nil
}

// ToEvidence 将检索结果绑定为 RUNBOOK 证据。
func (r Result) ToEvidence(runID string) *incidentmodel.Evidence {
	return &incidentmodel.Evidence{
		ID:        "ev_" + uuid.NewString(),
		RunID:     runID,
		Type:      incidentmodel.EvidenceRunbook,
		Source:    r.Chunk.Source,
		Content:   r.Chunk.Content,
		Metadata:  map[string]any{"doc_type": r.Chunk.DocType, "title": r.Chunk.Title, "score": r.Score},
		CreatedAt: time.Now(),
	}
}

// vectorRanks 向量检索（候选集大小 k，而非全量 chunks）并转为 1-based 排名。
func (h *Hybrid) vectorRanks(ctx context.Context, query string, k int) (map[string]int, error) {
	vecs, err := h.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return map[string]int{}, nil
	}
	results, err := h.vector.Search(ctx, vecs[0], k)
	if err != nil {
		return nil, err
	}
	ranks := make(map[string]int, len(results))
	for i, r := range results {
		ranks[r.ID] = i + 1
	}
	return ranks, nil
}

// lexicalRanks 词法打分并转为 1-based 排名。
func lexicalRanks(query string, chunks []*kmodel.Chunk) map[string]int {
	tokens := tokenize(query)
	type scored struct {
		id    string
		score float64
	}
	items := make([]scored, 0, len(chunks))
	for _, c := range chunks {
		if s := lexicalScore(tokens, c.Content); s > 0 {
			items = append(items, scored{id: c.ID, score: s})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].score > items[j].score })

	ranks := make(map[string]int, len(items))
	for i, it := range items {
		ranks[it.id] = i + 1
	}
	return ranks
}

// rrfScores 计算 Reciprocal Rank Fusion 分数（k 为常数，默认 60）。
func rrfScores(ranks map[string][]int, k int) map[string]float64 {
	scores := make(map[string]float64, len(ranks))
	for id, rs := range ranks {
		for _, r := range rs {
			scores[id] += 1.0 / float64(k+r)
		}
	}
	return scores
}

func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

func lexicalScore(tokens []string, content string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	lower := strings.ToLower(content)
	hits := 0
	for _, tok := range tokens {
		if strings.Contains(lower, tok) {
			hits++
		}
	}
	return float64(hits) / float64(len(tokens))
}
