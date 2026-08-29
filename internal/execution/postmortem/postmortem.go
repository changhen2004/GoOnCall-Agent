// Package postmortem 生成 Incident 复盘（设计文档 20）。
package postmortem

import (
	"fmt"
	"strings"

	"gooncall-agent/internal/incident/model"
)

// Generator 生成 Markdown 复盘。
type Generator struct{}

// New 创建复盘生成器。
func New() *Generator {
	return &Generator{}
}

// Generate 根据 Incident、根因、证据与处置生成复盘文档。
func (g *Generator) Generate(inc *model.Incident, rootCause string, evidence []string, resolution string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Incident %s\n\n", inc.ID)
	fmt.Fprintf(&b, "## Summary\n%s\n\n", inc.Title)
	if inc.Description != "" {
		fmt.Fprintf(&b, "## Impact\n%s\n\n", inc.Description)
	}
	fmt.Fprintf(&b, "## Root Cause\n%s\n\n", rootCause)

	fmt.Fprintf(&b, "## Evidence\n")
	if len(evidence) == 0 {
		fmt.Fprintf(&b, "- 无\n")
	} else {
		for _, e := range evidence {
			fmt.Fprintf(&b, "- %s\n", e)
		}
	}

	fmt.Fprintf(&b, "\n## Resolution\n%s\n\n", resolution)
	fmt.Fprintf(&b, "## Prevention\n- 增加相关指标告警\n- 增加 readiness 检查\n- 处置后增加自动验证\n")
	return b.String()
}
