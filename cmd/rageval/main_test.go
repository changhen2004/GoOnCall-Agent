package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadQrels_RejectsUnreviewedLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "qrels.jsonl")
	data := `{"query_id":"q1","query":"队列积压","component":"rabbitmq","relevant_doc_ids":["doc-1"],"reviewed":false}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadQrels(path)
	if err == nil || !strings.Contains(err.Error(), "requires human review") {
		t.Fatalf("loadQrels() error = %v", err)
	}
}
