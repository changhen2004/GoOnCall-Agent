package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

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
	store := vectorstore.NewMemory()

	ld := loader.New("docs", splitter.New(1000))
	chunks, err := ld.Load(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load knowledge docs: %w", err)
	}
	if len(chunks) == 0 {
		slog.Warn("no knowledge docs found under docs/")
		return nil, nil
	}

	h := retriever.NewHybrid(chunks, embedder, store)
	if err := h.Index(context.Background()); err != nil {
		return nil, fmt.Errorf("index knowledge: %w", err)
	}
	slog.Info("knowledge indexed", "chunks", len(chunks))
	return h, nil
}
