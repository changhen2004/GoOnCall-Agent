// Package knowledgecfg 把应用配置映射为知识库装配配置，供多个命令复用。
package knowledgecfg

import (
	"time"

	"gooncall-agent/configs"
	"gooncall-agent/internal/knowledge/rag"
)

// From 返回知识库装配配置。
func From(cfg *config.Config) rag.Config {
	return rag.Config{
		DocsDir:      cfg.RAG.DocsDir,
		QdrantURL:    cfg.Qdrant.URL,
		Collection:   cfg.Qdrant.Collection,
		Dim:          cfg.Qdrant.Dim,
		TopK:         cfg.RAG.TopK,
		CandidateK:   cfg.RAG.CandidateK,
		BuildTimeout: 2 * time.Minute,
		Embedding: rag.EmbeddingConfig{
			BaseURL: cfg.Embedding.BaseURL,
			APIKey:  cfg.Embedding.APIKey,
			Model:   cfg.Embedding.Model,
		},
		Reranker: rag.RerankerConfig{
			Enabled:  cfg.RAG.Reranker.Enabled,
			Provider: cfg.RAG.Reranker.Provider,
			BaseURL:  cfg.RAG.Reranker.BaseURL,
			APIKey:   cfg.RAG.Reranker.APIKey,
			Model:    cfg.RAG.Reranker.Model,
		},
	}
}
