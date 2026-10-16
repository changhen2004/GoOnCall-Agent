// Package evaluation 计算文档级检索指标。
package evaluation

import (
	"math"
	"sort"
	"strconv"
)

// Qrel 是一条查询及其人工确认的相关文档。
type Qrel struct {
	ID             string   `json:"query_id"`
	Query          string   `json:"query"`
	Component      string   `json:"component"`
	RelevantDocIDs []string `json:"relevant_doc_ids"`
	Reviewed       bool     `json:"reviewed"`
}

// Report 是一次文档级检索评测结果。
type Report struct {
	Queries            int                `json:"queries"`
	RecallAt           map[string]float64 `json:"recall_at"`
	MRR                float64            `json:"mrr"`
	NDCGAt5            float64            `json:"ndcg_at_5"`
	ComponentRecallAt5 map[string]float64 `json:"component_recall_at_5"`
	MissesAt5          []string           `json:"misses_at_5"`
}

// Evaluate 按文档 ID 去重后计算 Recall@k、MRR 与二元相关性 NDCG@5。
func Evaluate(qrels []Qrel, runs map[string][]string, ks ...int) Report {
	if len(ks) == 0 {
		ks = []int{3, 5, 8}
	}
	report := Report{
		Queries:            len(qrels),
		RecallAt:           make(map[string]float64, len(ks)),
		ComponentRecallAt5: make(map[string]float64),
	}
	if len(qrels) == 0 {
		return report
	}

	componentSum := make(map[string]float64)
	componentCount := make(map[string]int)
	for _, qrel := range qrels {
		relevant := stringSet(qrel.RelevantDocIDs)
		ranked := unique(runs[qrel.ID])
		for _, k := range ks {
			report.RecallAt[strconv.Itoa(k)] += recallAt(ranked, relevant, k)
		}
		recall5 := recallAt(ranked, relevant, 5)
		componentSum[qrel.Component] += recall5
		componentCount[qrel.Component]++
		if recall5 < 1 {
			report.MissesAt5 = append(report.MissesAt5, qrel.ID)
		}
		report.MRR += reciprocalRank(ranked, relevant)
		report.NDCGAt5 += ndcgAt(ranked, relevant, 5)
	}

	for _, k := range ks {
		key := strconv.Itoa(k)
		report.RecallAt[key] /= float64(len(qrels))
	}
	report.MRR /= float64(len(qrels))
	report.NDCGAt5 /= float64(len(qrels))
	for component, sum := range componentSum {
		report.ComponentRecallAt5[component] = sum / float64(componentCount[component])
	}
	sort.Strings(report.MissesAt5)
	return report
}

func recallAt(ranked []string, relevant map[string]struct{}, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	hits := 0
	for _, id := range ranked[:k] {
		if _, ok := relevant[id]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(relevant))
}

func reciprocalRank(ranked []string, relevant map[string]struct{}) float64 {
	for i, id := range ranked {
		if _, ok := relevant[id]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}

func ndcgAt(ranked []string, relevant map[string]struct{}, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(ranked) {
		k = len(ranked)
	}
	dcg := 0.0
	for i, id := range ranked[:k] {
		if _, ok := relevant[id]; ok {
			dcg += 1 / math.Log2(float64(i+2))
		}
	}
	ideal := len(relevant)
	if ideal > k {
		ideal = k
	}
	idcg := 0.0
	for i := 0; i < ideal; i++ {
		idcg += 1 / math.Log2(float64(i+2))
	}
	return dcg / idcg
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
