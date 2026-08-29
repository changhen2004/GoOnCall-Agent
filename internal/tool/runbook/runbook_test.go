package runbook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoc(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write doc: %v", err)
	}
}

const rabbitmqDoc = `# 消费者异常

RabbitMQ 队列积压时，检查 consumer count 是否下降。
`

const cpuDoc = `# CPU 打满

检查 CPU 使用率。
`

func TestSearch_ReturnsRelevantDocs(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "rabbitmq-backlog.md", rabbitmqDoc)
	writeDoc(t, dir, "cpu-throttle.md", cpuDoc)

	tool := New(dir)
	einoTool, err := tool.EinoTool()
	if err != nil {
		t.Fatalf("EinoTool() error = %v", err)
	}

	out, err := einoTool.InvokableRun(context.Background(), `{"query":"rabbitmq consumer backlog"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}

	if !strings.Contains(out, "rabbitmq-backlog.md") {
		t.Fatalf("expected rabbitmq-backlog.md in results, got: %s", out)
	}
	if strings.Contains(out, "cpu-throttle.md") {
		t.Fatalf("unexpected cpu-throttle.md in results: %s", out)
	}
}

func TestSearch_NoMatch(t *testing.T) {
	dir := t.TempDir()
	writeDoc(t, dir, "a.md", `# A

内容
`)

	tool := New(dir)
	einoTool, _ := tool.EinoTool()

	out, err := einoTool.InvokableRun(context.Background(), `{"query":"zzz qqq"}`)
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if out != "[]" {
		t.Fatalf("expected empty results [], got: %s", out)
	}
}
