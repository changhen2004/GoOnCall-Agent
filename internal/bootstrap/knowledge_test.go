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
	for _, field := range []string{"llm.base_url", "llm.embedding_model", "qdrant.url", "vector_store.provider"} {
		t.Run(field, func(t *testing.T) {
			cfg := config.Default()
			cfg.LLM.BaseURL = "http://embedding.invalid/v1"
			cfg.Qdrant.URL = "http://qdrant.invalid:6334"
			switch field {
			case "llm.base_url":
				cfg.LLM.BaseURL = " "
			case "llm.embedding_model":
				cfg.LLM.EmbeddingModel = " "
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
