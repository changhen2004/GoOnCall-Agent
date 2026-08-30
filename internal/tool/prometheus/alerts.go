package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

// AlertInfo 是简化后的单条告警（供 LLM 直接消费，避免原始 payload 噪音）。
type AlertInfo struct {
	AlertName   string            `json:"alertname"`
	Severity    string            `json:"severity"`
	Service     string            `json:"service,omitempty"`
	State       string            `json:"state"` // firing / pending / inactive
	Value       string            `json:"value,omitempty"`
	StartsAt    string            `json:"starts_at,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// AlertsInput 是告警查询入参。
type AlertsInput struct {
	// IncludeInactive 为 true 时同时返回 pending/inactive 告警；默认仅返回 firing。
	IncludeInactive bool `json:"include_inactive,omitempty" jsonschema:"description=是否包含非 firing（pending/inactive）告警，默认仅返回 firing 告警"`
}

// queryAlerts 查询 Prometheus 当前活跃告警（GET /api/v1/alerts）。
func (t *Tool) queryAlerts(ctx context.Context, in AlertsInput) ([]AlertInfo, error) {
	u := t.baseURL + "/api/v1/alerts"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus alerts request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus status %d: %s", resp.StatusCode, string(body))
	}

	var ar struct {
		Status string `json:"status"`
		Data   struct {
			Alerts []struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
				State       string            `json:"state"`
				Value       string            `json:"value"`
				ActiveAt    string            `json:"activeAt"`
			} `json:"alerts"`
		} `json:"data"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, fmt.Errorf("decode prometheus alerts response: %w", err)
	}
	if ar.Status != "success" {
		return nil, fmt.Errorf("prometheus alerts query failed: %s %s", ar.ErrorType, ar.Error)
	}

	out := make([]AlertInfo, 0, len(ar.Data.Alerts))
	for _, a := range ar.Data.Alerts {
		if !in.IncludeInactive && a.State != "firing" {
			continue
		}
		service := a.Labels["service"]
		if service == "" {
			service = a.Labels["job"]
		}
		out = append(out, AlertInfo{
			AlertName:   a.Labels["alertname"],
			Severity:    a.Labels["severity"],
			Service:     service,
			State:       a.State,
			Value:       a.Value,
			StartsAt:    a.ActiveAt,
			Summary:     a.Annotations["summary"],
			Description: a.Annotations["description"],
			Labels:      a.Labels,
		})
	}
	return out, nil
}

// AlertsEinoTool 返回告警查询 Eino 工具（只读，风险 LOW）。
//
// 诊断开始时先调用本工具确认当前 firing 告警集合，比让 LLM 凭空猜 PromQL 更可靠
// （参考 OnCallAgent 的 PrometheusAlertsTool）。
func (t *Tool) AlertsEinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"prometheus_alerts",
		"查询 Prometheus 当前活跃告警（/api/v1/alerts），返回 firing 告警的 alertname/severity/service/描述。诊断时优先调用本工具确认系统当前有哪些告警在响，再决定查询哪些指标。",
		t.queryAlerts,
	)
}
