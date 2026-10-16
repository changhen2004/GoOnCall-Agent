package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/knowledge/embedding"
	"gooncall-agent/internal/knowledge/loader"
	"gooncall-agent/internal/knowledge/retriever"
	"gooncall-agent/internal/knowledge/splitter"
	"gooncall-agent/internal/knowledge/vectorstore"
)

// buildRetriever 构建 RAG 混合检索器；配置或索引失败直接阻止启动。
func buildRetriever(cfg *config.Config) (retriever.Retriever, error) {
	if strings.TrimSpace(cfg.LLM.BaseURL) == "" {
		return nil, fmt.Errorf("llm.base_url (LLM_BASE_URL) is required for RAG")
	}
	if strings.TrimSpace(cfg.LLM.EmbeddingModel) == "" {
		return nil, fmt.Errorf("llm.embedding_model (LLM_EMBEDDING_MODEL) is required for RAG")
	}

	embedder := embedding.NewOpenAIEmbedder(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.EmbeddingModel)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	store, err := buildVectorStore(ctx, cfg)
	if err != nil {
		return nil, err
	}

	ld := loader.New("docs", splitter.New(1000))
	chunks, err := ld.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("load knowledge docs: %w", err)
	}
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no knowledge docs found under docs/")
	}

	h := retriever.NewHybrid(chunks, embedder, store, cfg.RAG.CandidateK)
	if err := h.Index(ctx); err != nil {
		return nil, fmt.Errorf("index knowledge: %w", err)
	}
	return h, nil
}

// buildVectorStore 使用显式配置的 Qdrant；内存向量库仅用于单元测试。
func buildVectorStore(ctx context.Context, cfg *config.Config) (vectorstore.VectorStore, error) {
	if cfg.VectorStore.Provider != "qdrant" {
		return nil, fmt.Errorf("vector_store.provider must be qdrant")
	}
	host, port, err := qdrantAddress(cfg.Qdrant.URL)
	if err != nil {
		return nil, err
	}
	qs, err := vectorstore.NewQdrant(host, port, cfg.Qdrant.Collection, cfg.Qdrant.Dim)
	if err != nil {
		return nil, fmt.Errorf("create qdrant store: %w", err)
	}
	if err := qs.EnsureCollection(ctx); err != nil {
		return nil, fmt.Errorf("ensure qdrant collection: %w", err)
	}
	slog.Info("vector store: qdrant", "collection", cfg.Qdrant.Collection)
	return qs, nil
}

// qdrantAddress 解析 gRPC 地址，省略端口时使用 6334。
func qdrantAddress(rawURL string) (string, int, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", 0, fmt.Errorf("qdrant.url (QDRANT_URL) is required for RAG")
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", 0, fmt.Errorf("qdrant.url must be a gRPC endpoint: host[:port] or http://host[:port]")
	}
	port := 6334
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", 0, fmt.Errorf("qdrant.url port must be between 1 and 65535")
		}
	}
	return u.Hostname(), port, nil
}
