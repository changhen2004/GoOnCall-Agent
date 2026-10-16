package bootstrap

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"gooncall-agent/internal/config"
)

func TestBuildRetriever_RequiresConfiguration(t *testing.T) {
	for _, field := range []string{"embedding.base_url", "embedding.model", "qdrant.url", "vector_store.provider"} {
		t.Run(field, func(t *testing.T) {
			cfg := config.Default()
			cfg.Embedding.BaseURL = "http://embedding.invalid/v1"
			cfg.Qdrant.URL = "http://qdrant.invalid:6334"
			switch field {
			case "embedding.base_url":
				cfg.Embedding.BaseURL = " "
			case "embedding.model":
				cfg.Embedding.Model = " "
			case "qdrant.url":
				cfg.Qdrant.URL = " "
			case "vector_store.provider":
				cfg.VectorStore.Provider = "memory"
			}
			r, err := BuildRunbookRetriever(cfg)
			if r != nil || err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("buildRetriever = %v, %v; want explicit %s error", r, err, field)
			}
		})
	}
}

func TestBuildRetriever_RequiresEnabledRerankerURL(t *testing.T) {
	cfg := config.Default()
	cfg.Embedding.BaseURL = "http://embedding.invalid/v1"
	cfg.Qdrant.URL = "http://qdrant.invalid:6334"
	cfg.RAG.Reranker.Enabled = true

	if _, err := BuildRunbookRetriever(cfg); err == nil || !strings.Contains(err.Error(), "RERANK_BASE_URL") {
		t.Fatalf("BuildRunbookRetriever() error = %v, want reranker URL error", err)
	}
}

func TestNewConfiguredReranker(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     config.RerankerConfig
		wantErr string
	}{
		{
			name: "qwen",
			cfg: config.RerankerConfig{
				Provider: config.RerankerProviderQwen,
				BaseURL:  "https://example.invalid/compatible-api/v1",
				APIKey:   "test-key",
				Model:    "qwen3-rerank",
			},
		},
		{name: "qwen without key", cfg: config.RerankerConfig{Provider: "qwen", BaseURL: "http://reranker", Model: "qwen3-rerank"}, wantErr: "RERANK_API_KEY"},
		{name: "qwen without model", cfg: config.RerankerConfig{Provider: "qwen", BaseURL: "http://reranker", APIKey: "test-key"}, wantErr: "RERANK_MODEL"},
		{name: "unknown provider", cfg: config.RerankerConfig{Provider: "unknown", BaseURL: "http://reranker"}, wantErr: "RERANK_PROVIDER"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reranker, err := newConfiguredReranker(tc.cfg)
			if tc.wantErr == "" && (reranker == nil || err != nil) {
				t.Fatalf("newConfiguredReranker() = %v, %v", reranker, err)
			}
			if tc.wantErr != "" && (reranker != nil || err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("newConfiguredReranker() = %v, %v; want %s error", reranker, err, tc.wantErr)
			}
		})
	}
}

func TestQdrantAddress(t *testing.T) {
	for _, tc := range []struct {
		raw, host string
		port      int
	}{
		{"localhost", "localhost", 6334},
		{"http://qdrant:16334/", "qdrant", 16334},
		{"[::1]:6334", "::1", 6334},
	} {
		host, port, err := qdrantAddress(tc.raw)
		if err != nil || host != tc.host || port != tc.port {
			t.Fatalf("qdrantAddress(%q) = %q, %d, %v", tc.raw, host, port, err)
		}
	}
	for _, raw := range []string{"", "http://", "http://host:bad", "http://host:0", "http://host:65536", "http://host/path", "https://host", "http://user:secret@host"} {
		if _, _, err := qdrantAddress(raw); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid endpoint must fail without echoing credentials: %q, %v", raw, err)
		}
	}
}

func TestBuildVectorStore_DoesNotFallbackOnServerError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer() // Reachable, but has no Qdrant service.
	defer server.Stop()
	go server.Serve(listener)
	cfg := config.Default()
	cfg.Qdrant.URL = listener.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	store, err := buildVectorStore(ctx, cfg)
	if store != nil || err == nil {
		t.Fatalf("buildVectorStore = %v, %v; want server error", store, err)
	}
}
