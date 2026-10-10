// Package verifier 提供处置后验证（设计文档 9.5）。
package verifier

import (
	"context"
	"fmt"
	"time"
)

// Metrics 是验证所需的指标快照。
type Metrics struct {
	ConsumerCount int     `json:"consumer_count"`
	QueueDepth    int     `json:"queue_depth"`
	ErrorRate     float64 `json:"error_rate"`
}

// Config 是验证阈值。
type Config struct {
	MinConsumers  int
	MaxQueueDepth int
	MaxErrorRate  float64
}

// DefaultConfig 返回默认阈值。
func DefaultConfig() Config {
	return Config{MinConsumers: 1, MaxQueueDepth: 1000, MaxErrorRate: 0.01}
}

// RunConfig 是连续验证配置（attempts / interval / required successes）。
type RunConfig struct {
	// Attempts 最多采集验证次数。
	Attempts int
	// Interval 两次采集之间的间隔。
	Interval time.Duration
	// RequiredSuccesses 判定成功所需的**连续**通过次数。
	RequiredSuccesses int
}

// DefaultRunConfig 返回"单次验证"配置（未配置 verification.* 时的行为）；
// 生产由 Bootstrap 按 verification.attempts / interval_seconds / required_successes 注入。
func DefaultRunConfig() RunConfig {
	return RunConfig{Attempts: 1, RequiredSuccesses: 1}
}

// normalized 只补齐零值：至少 1 次尝试、至少 1 次连续成功。
//
// 不把 RequiredSuccesses 降到 Attempts：配置 attempts=1 / required_successes=3 时，
// 静默降低阈值会让"验证一次就 RESOLVED"，与操作者要求的安全强度相反。
// 这类配置会在尝试耗尽后按"未达连续成功次数"失败。
func (c RunConfig) normalized() RunConfig {
	if c.Attempts <= 0 {
		c.Attempts = 1
	}
	if c.RequiredSuccesses <= 0 {
		c.RequiredSuccesses = 1
	}
	return c
}

// Check 是单条验证项。
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// Result 是验证结果。
type Result struct {
	Passed bool    `json:"passed"`
	Checks []Check `json:"checks"`
}

// Attempt 是一次采集 + 验证的结果（Error 非空表示采集失败）。
type Attempt struct {
	Attempt int     `json:"attempt"`
	Passed  bool    `json:"passed"`
	Metrics Metrics `json:"metrics"`
	Checks  []Check `json:"checks,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// SequenceResult 是连续验证结果。
type SequenceResult struct {
	Passed    bool      `json:"passed"`
	Successes int       `json:"successes"`
	Attempts  []Attempt `json:"attempts"`
	// Reason 是结构化失败/中断原因（采集错误、检查项未通过或连续次数不足）。
	Reason string `json:"reason,omitempty"`
}

// Verifier 验证处置是否生效。
type Verifier struct {
	cfg Config
}

// New 创建验证器。
func New(cfg Config) *Verifier {
	if cfg.MinConsumers <= 0 {
		cfg.MinConsumers = 1
	}
	if cfg.MaxErrorRate <= 0 {
		cfg.MaxErrorRate = 0.01
	}
	return &Verifier{cfg: cfg}
}

// Verify 校验指标是否恢复到正常范围。
func (v *Verifier) Verify(m Metrics) Result {
	checks := []Check{
		{
			Name:   "consumer_count",
			Passed: m.ConsumerCount >= v.cfg.MinConsumers,
			Detail: fmt.Sprintf("consumers=%d (min=%d)", m.ConsumerCount, v.cfg.MinConsumers),
		},
		{
			Name:   "queue_depth",
			Passed: m.QueueDepth <= v.cfg.MaxQueueDepth,
			Detail: fmt.Sprintf("queue_depth=%d (max=%d)", m.QueueDepth, v.cfg.MaxQueueDepth),
		},
		{
			Name:   "error_rate",
			Passed: m.ErrorRate <= v.cfg.MaxErrorRate,
			Detail: fmt.Sprintf("error_rate=%.4f (max=%.4f)", m.ErrorRate, v.cfg.MaxErrorRate),
		},
	}

	passed := true
	for _, c := range checks {
		if !c.Passed {
			passed = false
		}
	}
	return Result{Passed: passed, Checks: checks}
}

// VerifySequence 连续采集并验证：只有连续 RequiredSuccesses 次通过才算成功。
//
//   - 中途任意一次失败（含采集错误）都会把连续计数清零，但会继续尝试到 Attempts 用尽；
//   - 采集错误（Prometheus 不可达、空结果、NaN/Inf）一律按"本次未通过"处理，绝不按 0 通过；
//   - ctx 取消/超时立即返回，错误为 ctx.Err()：调用方不得据此宣称处置成功或失败。
func (v *Verifier) VerifySequence(ctx context.Context, g MetricGatherer, incidentID string, cfg RunConfig) (SequenceResult, error) {
	if g == nil {
		return SequenceResult{Reason: "no metric gatherer configured"}, fmt.Errorf("no metric gatherer configured")
	}
	cfg = cfg.normalized()
	res := SequenceResult{Attempts: make([]Attempt, 0, cfg.Attempts)}
	streak := 0

	for i := 1; i <= cfg.Attempts; i++ {
		if err := ctx.Err(); err != nil {
			res.Reason = "verification canceled: " + err.Error()
			return res, err
		}

		att := Attempt{Attempt: i}
		metrics, err := g.Gather(ctx, incidentID)
		if err != nil {
			att.Error = err.Error()
		} else {
			att.Metrics = metrics
			check := v.Verify(metrics)
			att.Passed = check.Passed
			att.Checks = check.Checks
		}
		res.Attempts = append(res.Attempts, att)

		if att.Passed {
			streak++
			res.Successes = streak
			if streak >= cfg.RequiredSuccesses {
				res.Passed = true
				res.Reason = ""
				return res, nil
			}
		} else {
			streak = 0
			res.Successes = 0
			if att.Error != "" {
				res.Reason = att.Error
			} else {
				res.Reason = "verification checks failed"
			}
		}

		if i < cfg.Attempts && cfg.Interval > 0 {
			select {
			case <-ctx.Done():
				res.Reason = "verification canceled: " + ctx.Err().Error()
				return res, ctx.Err()
			case <-time.After(cfg.Interval):
			}
		}
	}

	if res.Reason == "" {
		res.Reason = fmt.Sprintf("only %d consecutive successes, need %d", res.Successes, cfg.RequiredSuccesses)
	}
	return res, nil
}
