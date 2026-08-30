package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"gooncall-agent/internal/config"
	"gooncall-agent/internal/knowledge/embedding"
	"gooncall-agent/internal/knowledge/loader"
	"gooncall-agent/internal/knowledge/retriever"
	"gooncall-agent/internal/knowledge/splitter"
	"gooncall-agent/internal/knowledge/vectorstore"
)

// buildRetriever 构建 RAG 混合检索器（未配置 LLM/Embedding 时返回 nil）。
func buildRetriever(cfg *config.Config) (retriever.Retriever, error) {
	if cfg.LLM.BaseURL == "" || cfg.LLM.EmbeddingModel == "" {
		slog.Warn("RAG disabled (LLM_BASE_URL / LLM_EMBEDDING_MODEL empty), runbook.search uses lexical search")
		return nil, nil
	}

	embedder := embedding.NewOpenAIEmbedder(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.EmbeddingModel)

	store, err := buildVectorStore(cfg)
	if err != nil {
		return nil, err
	}

	ld := loader.New("docs", splitter.New(1000))
	chunks, err := ld.Load(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load knowledge docs: %w", err)
	}
	if len(chunks) == 0 {
		slog.Warn("no knowledge docs found under docs/")
		return nil, nil
	}

	h := retriever.NewHybrid(chunks, embedder, store, cfg.RAG.CandidateK)
	if err := h.Index(context.Background()); err != nil {
		return nil, fmt.Errorf("index knowledge: %w", err)
	}
	slog.Info("knowledge indexed", "chunks", len(chunks))
	return h, nil
}

// buildVectorStore 按配置构建向量存储（memory / qdrant）。
func buildVectorStore(cfg *config.Config) (vectorstore.VectorStore, error) {
	switch cfg.VectorStore.Provider {
	case "qdrant":
		host := qdrantHost(cfg.Qdrant.URL)
		qs, err := vectorstore.NewQdrant(host, 6334, cfg.Qdrant.Collection, cfg.Qdrant.Dim)
		if err != nil {
			return nil, fmt.Errorf("create qdrant store: %w", err)
		}
		if err := qs.EnsureCollection(context.Background()); err != nil {
			return nil, fmt.Errorf("ensure qdrant collection: %w", err)
		}
		slog.Info("vector store: qdrant", "collection", cfg.Qdrant.Collection)
		return qs, nil
	default:
		slog.Info("vector store: memory")
		return vectorstore.NewMemory(), nil
	}
}

// qdrantHost 从 URL 提取主机（去掉 scheme 与端口）。
func qdrantHost(rawURL string) string {
	u := rawURL
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.Index(u, "/"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, ":"); i >= 0 {
		u = u[:i]
	}
	return u
}
