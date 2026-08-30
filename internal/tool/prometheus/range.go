package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

// RangeInput 是 PromQL 范围查询入参。
type RangeInput struct {
	Query string `json:"query" jsonschema:"required,description=PromQL 查询表达式"`
	Start string `json:"start,omitempty" jsonschema:"description=开始时间（RFC3339 或 Unix 秒），默认当前时间前 30 分钟"`
	End   string `json:"end,omitempty" jsonschema:"description=结束时间（RFC3339 或 Unix 秒），默认当前时间"`
	Step  string `json:"step,omitempty" jsonschema:"description=采样步长（如 60s），默认 60s"`
}

// RangePoint 是单个采样点。
type RangePoint struct {
	Timestamp int64   `json:"ts"`
	Value     float64 `json:"value"`
}

// RangeSeries 是简化后的单条时间序列。
type RangeSeries struct {
	Metric    map[string]string `json:"metric"`
	Points    []RangePoint      `json:"points"`
	Truncated bool              `json:"truncated,omitempty"`
}

// queryRange 执行 PromQL 范围查询（GET /api/v1/query_range）。
// 为控制返回体积，每条序列最多保留最近 100 个采样点。
func (t *Tool) queryRange(ctx context.Context, in RangeInput) ([]RangeSeries, error) {
	if in.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	now := time.Now()
	start, err := parseTime(in.Start, now.Add(-30*time.Minute))
	if err != nil {
		return nil, err
	}
	end, err := parseTime(in.End, now)
	if err != nil {
		return nil, err
	}
	step := in.Step
	if step == "" {
		step = "60s"
	}

	u := t.baseURL + "/api/v1/query_range"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("query", in.Query)
	q.Set("start", strconv.FormatInt(start, 10))
	q.Set("end", strconv.FormatInt(end, 10))
	q.Set("step", step)
	req.URL.RawQuery = q.Encode()

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prometheus range request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus status %d: %s", resp.StatusCode, string(body))
	}

	var pr struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Values [][]any           `json:"values"`
			} `json:"result"`
		} `json:"data"`
		ErrorType string `json:"errorType"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, fmt.Errorf("decode prometheus range response: %w", err)
	}
	if pr.Status != "success" {
		return nil, fmt.Errorf("prometheus range query failed: %s %s", pr.ErrorType, pr.Error)
	}

	const maxPoints = 100
	out := make([]RangeSeries, 0, len(pr.Data.Result))
	for _, r := range pr.Data.Result {
		points := make([]RangePoint, 0, len(r.Values))
		truncated := false
		// 只保留尾部（最近）maxPoints 个点。
		vals := r.Values
		if len(vals) > maxPoints {
			vals = vals[len(vals)-maxPoints:]
			truncated = true
		}
		for _, v := range vals {
			if len(v) < 2 {
				continue
			}
			ts, ok1 := v[0].(float64)
			valStr, ok2 := v[1].(string)
			if !ok1 || !ok2 {
				continue
			}
			val, err := strconv.ParseFloat(valStr, 64)
			if err != nil {
				continue
			}
			points = append(points, RangePoint{Timestamp: int64(ts), Value: val})
		}
		if len(points) == 0 {
			continue
		}
		out = append(out, RangeSeries{Metric: r.Metric, Points: points, Truncated: truncated})
	}
	return out, nil
}

// RangeEinoTool 返回范围查询 Eino 工具（只读，风险 LOW）。
func (t *Tool) RangeEinoTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		"prometheus_range_query",
		"执行 PromQL 范围查询（/api/v1/query_range），返回时间序列走势（最近 100 个采样点）。用于观察指标在一段时间内的变化趋势，如错误率是否持续上升、恢复后是否回落。",
		t.queryRange,
	)
}

// parseTime 解析 RFC3339 或 Unix 秒时间；空值回退到 fallback。
func parseTime(s string, fallback time.Time) (int64, error) {
	if s == "" {
		return fallback.Unix(), nil
	}
	if ts, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ts, nil
	}
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q: %w", s, err)
	}
	return ts.Unix(), nil
}
