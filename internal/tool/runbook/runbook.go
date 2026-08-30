// Package runbook 提供 Runbook 知识检索工具。
//
// 默认使用词法检索；注入混合检索器（Phase 3 RAG）后升级为词法+向量混合检索。
package runbook

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"gooncall-agent/internal/knowledge/retriever"
)

// Tool 检索 Runbook 文档。
type Tool struct {
	docsDir   string
	retriever retriever.Retriever
	topK      int
}

// New 创建词法检索工具，docsDir 为文档根目录。
func New(docsDir string) *Tool {
	return &Tool{docsDir: docsDir, topK: 5}
}

// NewWithRetriever 创建基于混合检索（词法 + 向量）的工具，topK 为最终返回条数。
func NewWithRetriever(r retriever.Retriever, topK int) *Tool {
	if topK <= 0 {
		topK = 5
	}
	return &Tool{retriever: r, topK: topK}
}

// SearchInput 是检索入参。
type SearchInput struct {
	Query string `json:"query" jsonschema:"required,description=检索关键词"`
}

// SearchResult 是单条检索结果。
type SearchResult struct {
	Source  string  `json:"source"`
	Heading string  `json:"heading"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

type scoredDoc struct {
	SearchResult
	score float64
}

func (t *Tool) search(ctx context.Context, in SearchInput) ([]SearchResult, error) {
	if t.retriever != nil {
		results, err := t.retriever.Retrieve(ctx, in.Query, t.topK)
		if err != nil {
			return nil, err
		}
		out := make([]SearchResult, 0, len(results))
		for _, r := range results {
			out = append(out, SearchResult{
				Source:  r.Chunk.Source,
				Heading: r.Chunk.Title,
				Content: r.Chunk.Content,
				Score:   r.Score,
			})
		}
		return out, nil
	}
	return t.lexicalSearch(in.Query)
}

func (t *Tool) lexicalSearch(query string) ([]SearchResult, error) {
	files, err := listMarkdown(t.docsDir)
	if err != nil {
		return nil, err
	}

	queryTokens := tokenize(query)
	docs := make([]scoredDoc, 0)
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text := string(content)
		score := scoreDoc(queryTokens, text)
		if score <= 0 {
			continue
		}
		docs = append(docs, scoredDoc{
			SearchResult: SearchResult{
				Source:  filepath.Base(f),
				Heading: extractHeading(text),
				Content: truncate(text, 2000),
				Score:   score,
			},
			score: score,
		})
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].score > docs[j].score })
	if len(docs) > t.topK {
		docs = docs[:t.topK]
	}

	out := make([]SearchResult, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.SearchResult)
	}
	return out, nil
}

// EinoTool 返回 Eino 工具表示。
func (t *Tool) EinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"runbook_search",
		"检索运维 Runbook 知识库，返回与查询最相关的文档片段与相关度评分。用于查找已知故障的处理步骤。",
		t.search,
	)
}

func listMarkdown(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func tokenize(s string) []string {
	fields := strings.Fields(strings.ToLower(s))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

func scoreDoc(tokens []string, content string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	lower := strings.ToLower(content)
	hits := 0
	for _, tok := range tokens {
		if strings.Contains(lower, tok) {
			hits++
		}
	}
	return float64(hits) / float64(len(tokens))
}

func extractHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimPrefix(trimmed, "# ")
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
