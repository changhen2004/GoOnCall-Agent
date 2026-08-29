// Package verifier 提供处置后验证（设计文档 9.5）。
package verifier

import "fmt"

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
