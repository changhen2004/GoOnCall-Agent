package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gooncall-agent/internal/knowledge/retriever"
	"gooncall-agent/internal/tools"
)

// RetrieverProvider 延迟构建检索器；外部向量库不可达时不影响服务启动。
type RetrieverProvider func(ctx context.Context) (retriever.Retriever, error)

// KnowledgeOptions 是 Runbook 检索工具配置。
type KnowledgeOptions struct {
	Provider        RetrieverProvider
	TopK            int
	MaxTopK         int
	MaxQueryChars   int
	MaxContentChars int
	MaxResultBytes  int
	Timeout         time.Duration
}

// NewKnowledgeTool 构造知识检索工具。工具只接收查询条件，服务地址由服务端配置。
func NewKnowledgeTool(opts KnowledgeOptions) tools.Tool {
	if opts.TopK <= 0 {
		opts.TopK = 8
	}
	if opts.MaxTopK <= 0 {
		opts.MaxTopK = 10
	}
	if opts.MaxQueryChars <= 0 {
		opts.MaxQueryChars = 1000
	}
	if opts.MaxContentChars <= 0 {
		opts.MaxContentChars = 2000
	}
	if opts.MaxResultBytes <= 0 {
		opts.MaxResultBytes = 256 << 10
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}

	return tools.Tool{
		Name:        "knowledge_search",
		Description: "检索 Runbook/运维知识库，返回相关片段与来源；仅按需调用，不要求每次提问都检索。",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"query":{"type":"string","description":"检索问题或关键词"},
				"top_k":{"type":"integer","description":"返回条数，服务端会限制上限"}
			},
			"required":["query"]
		}`),
		Run: func(ctx context.Context, args json.RawMessage) (string, error) {
			var input struct {
				Query string `json:"query"`
				TopK  int    `json:"top_k"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return "", errors.New("参数不是有效的 JSON")
			}
			input.Query = strings.TrimSpace(input.Query)
			if input.Query == "" {
				return "", errors.New("query 不能为空")
			}
			if len([]rune(input.Query)) > opts.MaxQueryChars {
				return "", errors.New("query 超过服务端长度上限")
			}

			topK := input.TopK
			if topK <= 0 {
				topK = opts.TopK
			}
			if topK > opts.MaxTopK {
				topK = opts.MaxTopK
			}

			callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()

			retrieval, err := opts.Provider(callCtx)
			if err != nil || retrieval == nil {
				return "", errors.New("知识检索服务不可用")
			}
			results, err := retrieval.Retrieve(callCtx, input.Query, topK)
			if err != nil {
				return "", errors.New("知识检索失败")
			}

			total := len(results)
			if len(results) > topK {
				results = results[:topK]
			}
			truncated := total > len(results)

			hits := make([]knowledgeHit, 0, len(results))
			for _, result := range results {
				if result.Chunk == nil {
					continue
				}
				content := result.Chunk.Content
				contentTruncated := false
				if len([]rune(content)) > opts.MaxContentChars {
					content = truncateRunes(content, opts.MaxContentChars)
					contentTruncated = true
					truncated = true
				}
				hits = append(hits, knowledgeHit{
					Source:           result.Chunk.Source,
					DocID:            result.Chunk.DocID,
					SourceURL:        result.Chunk.SourceURL,
					Title:            result.Chunk.Title,
					Content:          content,
					Score:            result.Score,
					LastVerifiedAt:   result.Chunk.LastVerifiedAt,
					ContentTruncated: contentTruncated,
				})
			}

			response := knowledgeResponse{
				Results:     hits,
				ResultCount: total,
				Truncated:   truncated,
			}
			if response.Results == nil {
				response.Results = []knowledgeHit{}
			}
			if truncated {
				response.Notice = "结果已按服务端限制截断"
			}

			for {
				encoded, err := json.Marshal(response)
				if err != nil {
					return "", errors.New("结果编码失败")
				}
				if len(encoded) <= opts.MaxResultBytes || len(response.Results) == 0 {
					return string(encoded), nil
				}
				response.Results = response.Results[:len(response.Results)/2]
				response.Truncated = true
				response.Notice = "结果已按服务端限制截断"
			}
		},
	}
}

type knowledgeHit struct {
	Source           string  `json:"source"`
	DocID            string  `json:"doc_id"`
	SourceURL        string  `json:"source_url,omitempty"`
	Title            string  `json:"title"`
	Content          string  `json:"content"`
	Score            float64 `json:"score"`
	LastVerifiedAt   string  `json:"last_verified_at,omitempty"`
	ContentTruncated bool    `json:"content_truncated,omitempty"`
}

type knowledgeResponse struct {
	Results     []knowledgeHit `json:"results"`
	ResultCount int            `json:"result_count"`
	Truncated   bool           `json:"truncated"`
	Notice      string         `json:"notice,omitempty"`
}
