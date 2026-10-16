package reranker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTEIRerankAlignsScoresWithInput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/rerank" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var request struct {
			Query string   `json:"query"`
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Query != "queue backlog" || len(request.Texts) != 2 {
			t.Fatalf("request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"index":1,"score":0.9},{"index":0,"score":0.1}]`))
	}))
	defer server.Close()

	scores, err := NewTEI(server.URL, "test-key").Rerank(context.Background(), "queue backlog", []string{"a", "b"})
	if err != nil {
		t.Fatalf("Rerank() error = %v", err)
	}
	if len(scores) != 2 || scores[0] != 0.1 || scores[1] != 0.9 {
		t.Fatalf("scores = %v, want [0.1 0.9]", scores)
	}
}

func TestTEIRerankRejectsInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"index":0,"score":0.9},{"index":0,"score":0.8}]`))
	}))
	defer server.Close()

	_, err := NewTEI(server.URL, "").Rerank(context.Background(), "query", []string{"a", "b"})
	if err == nil || !strings.Contains(err.Error(), "invalid document index") {
		t.Fatalf("Rerank() error = %v, want invalid index error", err)
	}
}

func TestTEIRerankDoesNotExposeResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("secret-upstream-detail"))
	}))
	defer server.Close()

	_, err := NewTEI(server.URL, "secret-api-key").Rerank(context.Background(), "query", []string{"a"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("Rerank() error = %v", err)
	}
}
