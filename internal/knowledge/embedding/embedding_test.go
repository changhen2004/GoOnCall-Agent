package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbed_ReturnsInOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req embeddingRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Input) != 2 {
			t.Errorf("input len = %d, want 2", len(req.Input))
		}
		w.Write([]byte(`{"data":[{"embedding":[0.1,0.2],"index":0},{"embedding":[0.3,0.4],"index":1}]}`))
	}))
	defer server.Close()

	e := NewOpenAIEmbedder(server.URL, "sk-test", "text-embedding-3-small")
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if len(vecs) != 2 {
		t.Fatalf("len = %d, want 2", len(vecs))
	}
	if vecs[0][0] != 0.1 || vecs[1][0] != 0.3 {
		t.Fatalf("vecs = %v", vecs)
	}
}

func TestEmbed_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	e := NewOpenAIEmbedder(server.URL, "k", "m")
	if _, err := e.Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("expected error for 500")
	}
}
