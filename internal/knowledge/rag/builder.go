// Package rag 装配 Runbook 知识库的加载、索引与混合检索。
//
// 服务启动只校验配置；真正的 Qdrant/Embedding 连接在首次检索或导入时建立，
// 因此外部知识服务暂不可达不会阻止 Agent 登录和其他工具使用。
package rag

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gooncall-agent/internal/knowledge/embedding"
	"gooncall-agent/internal/knowledge/loader"
	kmodel "gooncall-agent/internal/knowledge/model"
	"gooncall-agent/internal/knowledge/reranker"
	"gooncall-agent/internal/knowledge/retriever"
	"gooncall-agent/internal/knowledge/splitter"
	"gooncall-agent/internal/knowledge/vectorstore"
)

// EmbeddingConfig 是向量模型配置。
type EmbeddingConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

// RerankerConfig 是候选重排配置。
type RerankerConfig struct {
	Enabled  bool
	Provider string
	BaseURL  string
	APIKey   string
	Model    string
}

// Config 是知识库装配配置。
type Config struct {
	DocsDir      string
	QdrantURL    string
	Collection   string
	Dim          uint64
	TopK         int
	CandidateK   int
	Embedding    EmbeddingConfig
	Reranker     RerankerConfig
	BuildTimeout time.Duration
}

// Builder 延迟构建检索器并缓存成功结果。
type Builder struct {
	cfg Config

	mu        sync.Mutex
	retriever retriever.Retriever
	chunks    int
}

// New 校验配置形状并创建 Builder；不连接外部服务。
func New(cfg Config) (*Builder, error) {
	if strings.TrimSpace(cfg.DocsDir) == "" {
		cfg.DocsDir = "docs"
	}
	if cfg.Collection == "" {
		cfg.Collection = "gooncall_knowledge"
	}
	if cfg.Dim == 0 {
		cfg.Dim = 768
	}
	if cfg.TopK <= 0 {
		cfg.TopK = 8
	}
	if cfg.CandidateK <= 0 {
		cfg.CandidateK = 20
	}
	if cfg.BuildTimeout <= 0 {
		cfg.BuildTimeout = 2 * time.Minute
	}
	if cfg.Embedding.BaseURL == "" || cfg.Embedding.Model == "" {
		return nil, errors.New("知识库需要 embedding.base_url 和 embedding.model")
	}
	if _, _, err := qdrantAddress(cfg.QdrantURL); err != nil {
		return nil, err
	}
	return &Builder{cfg: cfg}, nil
}

// Get 返回检索器；首次调用或上次构建失败后重试构建。
func (b *Builder) Get(ctx context.Context) (retriever.Retriever, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.retriever != nil {
		return b.retriever, nil
	}
	retrieval, chunks, err := b.build(ctx)
	if err != nil {
		return nil, err
	}
	b.retriever = retrieval
	b.chunks = len(chunks)
	return retrieval, nil
}

// Import 构建并索引知识库，返回索引的 Runbook 片段数。
func (b *Builder) Import(ctx context.Context) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.retriever != nil {
		return b.chunks, nil
	}
	retrieval, chunks, err := b.build(ctx)
	if err != nil {
		return 0, err
	}
	b.retriever = retrieval
	b.chunks = len(chunks)
	return b.chunks, nil
}

// TopK 返回配置的默认检索条数。
func (b *Builder) TopK() int { return b.cfg.TopK }

func (b *Builder) build(ctx context.Context) (retriever.Retriever, []*kmodel.Chunk, error) {
	buildCtx, cancel := context.WithTimeout(ctx, b.cfg.BuildTimeout)
	defer cancel()

	host, port, err := qdrantAddress(b.cfg.QdrantURL)
	if err != nil {
		return nil, nil, err
	}
	store, err := vectorstore.NewQdrant(host, port, b.cfg.Collection, b.cfg.Dim)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Qdrant 客户端: %w", err)
	}
	if err := store.EnsureCollection(buildCtx); err != nil {
		return nil, nil, fmt.Errorf("连接 Qdrant: %w", err)
	}

	load := loader.New(b.cfg.DocsDir, splitter.New(1000))
	all, err := load.Load(buildCtx)
	if err != nil {
		return nil, nil, fmt.Errorf("加载知识文档: %w", err)
	}
	runbooks := make([]*kmodel.Chunk, 0, len(all))
	for _, chunk := range all {
		if chunk.DocType == "runbook" {
			runbooks = append(runbooks, chunk)
		}
	}
	if len(runbooks) == 0 {
		return nil, nil, fmt.Errorf("%s 下没有 Runbook 文档", b.cfg.DocsDir)
	}

	embedder := embedding.NewOpenAIEmbedder(
		b.cfg.Embedding.BaseURL, b.cfg.Embedding.APIKey, b.cfg.Embedding.Model)
	hybrid := retriever.NewHybrid(runbooks, embedder, store, b.cfg.CandidateK)
	if b.cfg.Reranker.Enabled {
		candidate, err := newReranker(b.cfg.Reranker)
		if err != nil {
			return nil, nil, err
		}
		hybrid.WithReranker(candidate, b.cfg.CandidateK)
	}
	if err := hybrid.Index(buildCtx); err != nil {
		return nil, nil, fmt.Errorf("索引知识库: %w", err)
	}
	return hybrid, runbooks, nil
}

func newReranker(cfg RerankerConfig) (retriever.Reranker, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Provider)) {
	case "", "tei":
		return reranker.NewTEI(cfg.BaseURL, cfg.APIKey), nil
	case "qwen":
		return reranker.NewQwen(cfg.BaseURL, cfg.APIKey, cfg.Model), nil
	default:
		return nil, errors.New("rag.reranker.provider 必须为 tei 或 qwen")
	}
}

// qdrantAddress 解析 gRPC 地址，省略端口时使用 6334。
func qdrantAddress(rawURL string) (string, int, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", 0, errors.New("知识库需要 qdrant.url")
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "http" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", 0, errors.New("qdrant.url 必须是 host[:port] 或 http://host[:port]")
	}
	port := 6334
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", 0, errors.New("qdrant.url 端口必须在 1-65535 之间")
		}
	}
	return u.Hostname(), port, nil
}
