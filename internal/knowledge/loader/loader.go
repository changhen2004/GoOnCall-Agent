// Package loader 从 docs 目录加载 Markdown 文档并切分为知识片段。
package loader

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	kmodel "gooncall-agent/internal/knowledge/model"
	"gooncall-agent/internal/knowledge/splitter"
)

// Loader 加载知识文档。
type Loader struct {
	docsDir  string
	splitter *splitter.Splitter
}

// New 创建 Loader，docsDir 为文档根目录。
func New(docsDir string, sp *splitter.Splitter) *Loader {
	if sp == nil {
		sp = splitter.New(0)
	}
	return &Loader{docsDir: docsDir, splitter: sp}
}

// frontmatter 是文档头部元数据（YAML）。
type frontmatter struct {
	Service  string   `yaml:"service"`
	Severity string   `yaml:"severity"`
	Title    string   `yaml:"title"`
	Tags     []string `yaml:"tags"`
}

// Load 递归加载 docs 目录下所有 .md 文件，切分为 Chunk。
func (l *Loader) Load(_ context.Context) ([]*kmodel.Chunk, error) {
	chunks := make([]*kmodel.Chunk, 0)
	err := filepath.WalkDir(l.docsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		rel, _ := filepath.Rel(l.docsDir, path)
		docType := docTypeFromPath(rel)
		source := filepath.Base(path)

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		fm, body := parseFrontmatter(string(content))
		title := fm.Title
		if title == "" {
			title = extractHeading(body)
		}

		for i, piece := range l.splitter.Split(body) {
			chunks = append(chunks, &kmodel.Chunk{
				ID:       fmt.Sprintf("%s#%d", source, i),
				Source:   source,
				DocType:  docType,
				Service:  fm.Service,
				Title:    title,
				Content:  piece,
				Severity: fm.Severity,
				Tags:     fm.Tags,
			})
		}
		return nil
	})
	return chunks, err
}

// docTypeFromPath 根据相对路径首段推导文档类型。
func docTypeFromPath(rel string) string {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	if len(parts) == 0 {
		return "unknown"
	}
	switch parts[0] {
	case "runbooks":
		return "runbook"
	case "incidents":
		return "incident"
	case "postmortems":
		return "postmortem"
	case "architecture":
		return "architecture"
	default:
		return "unknown"
	}
}

// parseFrontmatter 解析文档头部 `---` 包裹的 YAML 元数据，返回元数据与正文。
func parseFrontmatter(text string) (frontmatter, string) {
	var fm frontmatter
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		return fm, text
	}

	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return fm, text
	}

	yamlLines := make([]string, 0)
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
		yamlLines = append(yamlLines, lines[i])
	}
	if end == -1 {
		return fm, text
	}

	_ = yaml.Unmarshal([]byte(strings.Join(yamlLines, "\n")), &fm)
	body := strings.Join(lines[end+1:], "\n")
	return fm, strings.TrimSpace(body)
}

// extractHeading 返回正文首个一级标题。
func extractHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimPrefix(trimmed, "# ")
		}
	}
	return ""
}
