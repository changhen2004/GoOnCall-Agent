package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gooncall-agent/internal/tools"
	"gooncall-agent/internal/tools/prometheus"
)

const (
	defaultPrometheusMaxSeries = 50
	defaultPrometheusMaxAlerts = 50
	maxRangeSpan               = 24 * time.Hour
	maxRangePoints             = 100
	maxQueryChars              = 2000
)

// PrometheusOptions 是只读监控工具的配置。
type PrometheusOptions struct {
	URL            string
	Timeout        time.Duration
	MaxSeries      int
	MaxAlerts      int
	MaxResultBytes int
}

// NewPrometheusTools 构造即时查询、范围查询和告警查询工具。
func NewPrometheusTools(opts PrometheusOptions) []tools.Tool {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.MaxSeries <= 0 {
		opts.MaxSeries = defaultPrometheusMaxSeries
	}
	if opts.MaxAlerts <= 0 {
		opts.MaxAlerts = defaultPrometheusMaxAlerts
	}
	if opts.MaxResultBytes <= 0 {
		opts.MaxResultBytes = 256 << 10
	}
	client := prometheus.New(opts.URL)

	return []tools.Tool{
		{
			Name:        "prometheus_query",
			Description: "执行 Prometheus 即时查询（PromQL），返回指标结果；服务地址由服务端配置。",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"query":{"type":"string","description":"PromQL 查询表达式"}},
				"required":["query"]
			}`),
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				return runPrometheusInstant(ctx, client, opts, args)
			},
		},
		{
			Name:        "prometheus_range_query",
			Description: "执行 Prometheus 范围查询（PromQL），时间跨度最多 24 小时，每条序列最多 100 个采样点。",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"PromQL 查询表达式"},
					"start":{"type":"string","description":"开始时间（RFC3339 或 Unix 秒），默认 30 分钟前"},
					"end":{"type":"string","description":"结束时间（RFC3339 或 Unix 秒），默认当前时间"},
					"step":{"type":"string","description":"采样步长（如 60s），默认 60s，最小 1s"}
				},
				"required":["query"]
			}`),
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				return runPrometheusRange(ctx, client, opts, args)
			},
		},
		{
			Name:        "prometheus_alerts",
			Description: "查询 Prometheus 当前告警；默认只返回 firing 告警，可选择包含 pending/inactive。",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"include_inactive":{"type":"boolean","description":"是否包含非 firing 告警"}}
			}`),
			Run: func(ctx context.Context, args json.RawMessage) (string, error) {
				return runPrometheusAlerts(ctx, client, opts, args)
			},
		},
	}
}

func runPrometheusInstant(ctx context.Context, client *prometheus.Tool, opts PrometheusOptions, args json.RawMessage) (string, error) {
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", errors.New("参数不是有效的 JSON")
	}
	if err := validatePromQuery(input.Query); err != nil {
		return "", err
	}

	callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	raw, err := client.Run(callCtx, string(args))
	if err != nil {
		return "", prometheusError(err)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return "", errors.New("Prometheus 响应格式错误")
	}
	if data == nil {
		data = make(map[string]any)
	}
	clampPrometheusResult(data, opts.MaxSeries, opts.MaxResultBytes)
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", errors.New("Prometheus 响应格式错误")
	}
	return string(encoded), nil
}

func runPrometheusRange(ctx context.Context, client *prometheus.Tool, opts PrometheusOptions, args json.RawMessage) (string, error) {
	var input struct {
		Query string `json:"query"`
		Start string `json:"start"`
		End   string `json:"end"`
		Step  string `json:"step"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return "", errors.New("参数不是有效的 JSON")
	}
	if err := validatePromQuery(input.Query); err != nil {
		return "", err
	}
	start, end, step, err := normalizeRange(input.Start, input.End, input.Step)
	if err != nil {
		return "", err
	}
	request, err := json.Marshal(map[string]string{
		"query": input.Query,
		"start": strconv.FormatInt(start.Unix(), 10),
		"end":   strconv.FormatInt(end.Unix(), 10),
		"step":  step.String(),
	})
	if err != nil {
		return "", errors.New("参数不是有效的 JSON")
	}

	callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	raw, err := client.RunRange(callCtx, string(request))
	if err != nil {
		return "", prometheusError(err)
	}

	var series []prometheus.RangeSeries
	if err := json.Unmarshal([]byte(raw), &series); err != nil {
		return "", errors.New("Prometheus 响应格式错误")
	}
	total := len(series)
	truncated := false
	for _, item := range series {
		truncated = truncated || item.Truncated
	}
	if len(series) > opts.MaxSeries {
		series = series[:opts.MaxSeries]
		truncated = true
	}

	response := struct {
		Series      []prometheus.RangeSeries `json:"series"`
		ResultCount int                      `json:"result_count"`
		Truncated   bool                     `json:"truncated"`
		Notice      string                   `json:"notice,omitempty"`
	}{Series: series, ResultCount: total, Truncated: truncated}
	if response.Series == nil {
		response.Series = []prometheus.RangeSeries{}
	}
	if truncated {
		response.Notice = "结果已按服务端限制截断"
	}
	for {
		encoded, err := json.Marshal(response)
		if err != nil {
			return "", errors.New("Prometheus 响应格式错误")
		}
		if len(encoded) <= opts.MaxResultBytes || len(response.Series) == 0 {
			return string(encoded), nil
		}
		response.Series = response.Series[:len(response.Series)/2]
		response.Truncated = true
		response.Notice = "结果已按服务端限制截断"
	}
}

func runPrometheusAlerts(ctx context.Context, client *prometheus.Tool, opts PrometheusOptions, args json.RawMessage) (string, error) {
	var input struct {
		IncludeInactive bool `json:"include_inactive"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &input); err != nil {
			return "", errors.New("参数不是有效的 JSON")
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	raw, err := client.RunAlerts(callCtx, string(args))
	if err != nil {
		return "", prometheusError(err)
	}

	var alerts []prometheus.AlertInfo
	if err := json.Unmarshal([]byte(raw), &alerts); err != nil {
		return "", errors.New("Prometheus 响应格式错误")
	}
	total := len(alerts)
	truncated := false
	if len(alerts) > opts.MaxAlerts {
		alerts = alerts[:opts.MaxAlerts]
		truncated = true
	}
	response := struct {
		Alerts      []prometheus.AlertInfo `json:"alerts"`
		ResultCount int                    `json:"result_count"`
		Truncated   bool                   `json:"truncated"`
	}{Alerts: alerts, ResultCount: total, Truncated: truncated}
	if response.Alerts == nil {
		response.Alerts = []prometheus.AlertInfo{}
	}
	for {
		encoded, err := json.Marshal(response)
		if err != nil {
			return "", errors.New("Prometheus 响应格式错误")
		}
		if len(encoded) <= opts.MaxResultBytes || len(response.Alerts) == 0 {
			return string(encoded), nil
		}
		response.Alerts = response.Alerts[:len(response.Alerts)/2]
		response.Truncated = true
	}
}

func validatePromQuery(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return errors.New("query 不能为空")
	}
	if len([]rune(query)) > maxQueryChars {
		return fmt.Errorf("query 超过 %d 个字符上限", maxQueryChars)
	}
	return nil
}

// normalizeRange 限制时间跨度与采样密度，避免先加载海量数据再截断。
func normalizeRange(rawStart, rawEnd, rawStep string) (time.Time, time.Time, time.Duration, error) {
	now := time.Now()
	start, err := parseTimeArg(rawStart, now.Add(-30*time.Minute))
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	end, err := parseTimeArg(rawEnd, now)
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, 0, errors.New("end 必须晚于 start")
	}
	span := end.Sub(start)
	if span > maxRangeSpan {
		return time.Time{}, time.Time{}, 0, errors.New("时间范围不能超过 24 小时")
	}

	step := 60 * time.Second
	if strings.TrimSpace(rawStep) != "" {
		step, err = time.ParseDuration(rawStep)
		if err != nil {
			return time.Time{}, time.Time{}, 0, errors.New("step 必须是合法时长（如 60s）")
		}
	}
	if step < time.Second || step > time.Hour {
		return time.Time{}, time.Time{}, 0, errors.New("step 必须在 1s 到 1h 之间")
	}
	if minStep := span / maxRangePoints; step < minStep {
		// 向上取整到秒，保证 Prometheus 能解析步长。
		step = ((minStep + time.Second - 1) / time.Second) * time.Second
	}
	return start, end, step, nil
}

func parseTimeArg(raw string, fallback time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	if ts, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(ts, 0), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New("时间必须是 RFC3339 或 Unix 秒")
	}
	return t, nil
}

func clampPrometheusResult(data map[string]any, maxSeries, maxBytes int) {
	result, ok := data["result"].([]any)
	if !ok {
		data["result_count"] = 0
		data["truncated"] = false
		return
	}
	total := len(result)
	truncated := false
	if len(result) > maxSeries {
		result = result[:maxSeries]
		truncated = true
	}

	for {
		data["result"] = result
		if len(result) == 0 {
			break
		}
		encoded, err := json.Marshal(data)
		if err != nil || len(encoded) <= maxBytes {
			break
		}
		truncated = true
		result = result[:len(result)/2]
	}
	if len(result) == 0 {
		data["result"] = []any{}
	}
	data["result_count"] = total
	data["truncated"] = truncated
	if truncated {
		data["notice"] = "结果已按服务端限制截断"
	}
}

func prometheusError(err error) error {
	var queryErr *prometheus.QueryError
	switch {
	case errors.As(err, &queryErr):
		return fmt.Errorf("Prometheus 查询失败: %s", truncateRunes(queryErr.Detail, 300))
	case errors.Is(err, prometheus.ErrUnavailable):
		return errors.New("Prometheus 服务不可达")
	case errors.Is(err, prometheus.ErrInvalidResponse):
		return errors.New("Prometheus 响应格式错误")
	default:
		return errors.New("Prometheus 查询失败")
	}
}
