package loader

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gooncall-agent/internal/knowledge/splitter"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const rabbitmqDoc = `---
service: GoCommunity
severity: P1
tags: [rabbitmq, worker]
---
# 消费者异常

RabbitMQ 队列积压时检查 consumer count。
`

const incidentDoc = `# 事件一

内容
`

const postmortemDoc = `# 复盘一

内容
`

func TestLoad_ExtractsMetadataAndChunks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, filepath.Join("runbooks", "rabbitmq.md"), rabbitmqDoc)

	ld := New(dir, splitter.New(1000))
	chunks, err := ld.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("chunks = %d, want 1", len(chunks))
	}
	c := chunks[0]
	if c.DocType != "runbook" {
		t.Errorf("DocType = %q, want runbook", c.DocType)
	}
	if c.Service != "GoCommunity" {
		t.Errorf("Service = %q, want GoCommunity", c.Service)
	}
	if c.Severity != "P1" {
		t.Errorf("Severity = %q, want P1", c.Severity)
	}
	if c.Title != "消费者异常" {
		t.Errorf("Title = %q, want 消费者异常", c.Title)
	}
	if len(c.Tags) != 2 {
		t.Errorf("Tags = %v, want 2", c.Tags)
	}
	if c.ID == "" || c.Source != "rabbitmq.md" {
		t.Errorf("ID/Source = %q/%q", c.ID, c.Source)
	}
}

func TestLoad_DocTypeFromDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, filepath.Join("incidents", "inc1.md"), incidentDoc)
	writeFile(t, dir, filepath.Join("postmortems", "pm1.md"), postmortemDoc)

	ld := New(dir, splitter.New(1000))
	chunks, err := ld.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	types := map[string]bool{}
	for _, c := range chunks {
		types[c.DocType] = true
	}
	if !types["incident"] || !types["postmortem"] {
		t.Fatalf("doc types = %v", types)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	const input = `# 标题

正文
`
	const want = `# 标题

正文`
	_, body := parseFrontmatter(input)
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestParseFrontmatter_WithFrontmatter(t *testing.T) {
	const input = `---
service: SVC
tags: [a, b]
---
正文
`
	fm, body := parseFrontmatter(input)
	if fm.Service != "SVC" {
		t.Fatalf("service = %q", fm.Service)
	}
	if body != "正文" {
		t.Fatalf("body = %q", body)
	}
}
