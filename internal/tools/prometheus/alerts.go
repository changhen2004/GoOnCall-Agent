package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w", ErrUnavailable)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= http.StatusInternalServerError {
			return nil, fmt.Errorf("%w", ErrUnavailable)
		}
		return nil, &QueryError{Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
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
		return nil, fmt.Errorf("%w", ErrInvalidResponse)
	}
	if ar.Status != "success" {
		return nil, &QueryError{Detail: "alerts " + ar.ErrorType + " " + ar.Error}
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

// RunAlerts 解析明确输入结构，返回可供 Agent 使用的 JSON 结果。
func (t *Tool) RunAlerts(ctx context.Context, arguments string) (string, error) {
	var input AlertsInput
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return "", err
	}
	result, err := t.queryAlerts(ctx, input)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	return string(data), err
}
