package evaluation

import "testing"

func TestEvaluate_DeduplicatesDocumentsAndReportsMisses(t *testing.T) {
	qrels := []Qrel{
		{ID: "q1", Component: "rabbitmq", RelevantDocIDs: []string{"doc-a"}},
		{ID: "q2", Component: "postgresql", RelevantDocIDs: []string{"doc-b", "doc-c"}},
	}
	runs := map[string][]string{
		"q1": {"doc-x", "doc-a", "doc-a"},
		"q2": {"doc-b"},
	}

	report := Evaluate(qrels, runs, 1, 5)
	if report.RecallAt["1"] != 0.25 {
		t.Fatalf("Recall@1 = %v, want 0.25", report.RecallAt["1"])
	}
	if report.RecallAt["5"] != 0.75 {
		t.Fatalf("Recall@5 = %v, want 0.75", report.RecallAt["5"])
	}
	if report.MRR != 0.75 {
		t.Fatalf("MRR = %v, want 0.75", report.MRR)
	}
	if len(report.MissesAt5) != 1 || report.MissesAt5[0] != "q2" {
		t.Fatalf("MissesAt5 = %v, want [q2]", report.MissesAt5)
	}
	if report.ComponentRecallAt5["rabbitmq"] != 1 || report.ComponentRecallAt5["postgresql"] != 0.5 {
		t.Fatalf("component recall = %v", report.ComponentRecallAt5)
	}
}
