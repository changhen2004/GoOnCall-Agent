package reranker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQwenRerankAlignsScoresWithInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/services/rerank/text-rerank/text-rerank" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var request struct {
			Model string `json:"model"`
			Input struct {
				Query     string   `json:"query"`
				Documents []string `json:"documents"`
			} `json:"input"`
			Parameters struct {
				ReturnDocuments bool `json:"return_documents"`
				TopN            *int `json:"top_n"`
			} `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "qwen3-rerank" || request.Input.Query != "queue backlog" || len(request.Input.Documents) != 2 {
			t.Fatalf("request = %+v", request)
		}
		if request.Parameters.ReturnDocuments || request.Parameters.TopN != nil {
			t.Fatalf("parameters = %+v, want return_documents=false and top_n omitted", request.Parameters)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.1}]},"usage":{"total_tokens":12}}`))
	}))
	defer server.Close()

	scores, err := NewQwen(server.URL+"/api/v1", "test-key", "qwen3-rerank").Rerank(context.Background(), "queue backlog", []string{"a", "b"})
	if err != nil {
		t.Fatalf("Rerank() error = %v", err)
	}
	if len(scores) != 2 || scores[0] != 0.1 || scores[1] != 0.9 {
		t.Fatalf("scores = %v, want [0.1 0.9]", scores)
	}
}

func TestQwenRerankRejectsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"output":{"results":[{"index":0,"relevance_score":0.9},{"index":0,"relevance_score":0.8}]}}`))
	}))
	defer server.Close()

	_, err := NewQwen(server.URL, "test-key", "qwen3-rerank").Rerank(context.Background(), "query", []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "invalid document index") {
		t.Fatalf("Rerank() error = %v, want invalid index error", err)
	}
}

func TestQwenRerankDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("secret-upstream-detail"))
	}))
	defer server.Close()

	_, err := NewQwen(server.URL, "secret-api-key", "qwen3-rerank").Rerank(context.Background(), "query", []string{"a"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("Rerank() error = %v", err)
	}
}
