// Package incident 提供历史 Incident 检索工具。
package incident

import (
	"context"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"gooncall-agent/internal/incident/model"
	"gooncall-agent/internal/incident/repository"
)

// Tool 检索历史 Incident（Read Only，风险 LOW）。
type Tool struct {
	repo repository.Repository
}

// New 创建 Incident 历史检索工具。
func New(repo repository.Repository) *Tool {
	return &Tool{repo: repo}
}

// HistoryInput 是历史检索入参。
type HistoryInput struct {
	Service   string `json:"service,omitempty" jsonschema:"description=服务名"`
	AlertName string `json:"alert_name,omitempty" jsonschema:"description=告警名"`
	Status    string `json:"status,omitempty" jsonschema:"description=状态，可选 OPEN/INVESTIGATING/RESOLVED 等"`
}

// HistoryResult 是单条历史 Incident 摘要。
type HistoryResult struct {
	ID         string     `json:"id"`
	Service    string     `json:"service"`
	AlertName  string     `json:"alert_name"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	Severity   string     `json:"severity"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

func (t *Tool) search(ctx context.Context, in HistoryInput) ([]HistoryResult, error) {
	list, err := t.repo.List(ctx, repository.ListFilter{
		Service: in.Service,
		Status:  model.Status(in.Status),
		Limit:   50,
	})
	if err != nil {
		return nil, err
	}

	out := make([]HistoryResult, 0, len(list))
	for _, inc := range list {
		if in.AlertName != "" && inc.AlertName != in.AlertName {
			continue
		}
		out = append(out, HistoryResult{
			ID:         inc.ID,
			Service:    inc.Service,
			AlertName:  inc.AlertName,
			Title:      inc.Title,
			Status:     string(inc.Status),
			Severity:   inc.Severity,
			StartedAt:  inc.StartedAt,
			ResolvedAt: inc.ResolvedAt,
		})
	}
	return out, nil
}

// EinoTool 返回 Eino 工具表示。
func (t *Tool) EinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"incident_history",
		"检索同服务、同告警或同状态的历史 Incident，用于参考历史根因与处理方式。",
		t.search,
	)
}
