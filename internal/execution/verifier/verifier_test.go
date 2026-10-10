package verifier

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVerify_AllPassed(t *testing.T) {
	v := New(DefaultConfig())
	res := v.Verify(Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001})
	if !res.Passed {
		t.Fatalf("Verify() = %+v, want passed", res)
	}
	if len(res.Checks) != 3 {
		t.Fatalf("checks = %d, want 3", len(res.Checks))
	}
}

func TestVerify_ConsumerCountFailed(t *testing.T) {
	v := New(DefaultConfig())
	res := v.Verify(Metrics{ConsumerCount: 0, QueueDepth: 100, ErrorRate: 0.001})
	if res.Passed {
		t.Fatal("Verify() passed, want failed (consumer_count=0)")
	}
	if res.Checks[0].Passed {
		t.Fatal("consumer_count check should fail")
	}
}

func TestVerify_QueueDepthFailed(t *testing.T) {
	v := New(Config{MinConsumers: 1, MaxQueueDepth: 100, MaxErrorRate: 0.01})
	res := v.Verify(Metrics{ConsumerCount: 5, QueueDepth: 500, ErrorRate: 0.001})
	if res.Passed {
		t.Fatal("Verify() passed, want failed (queue_depth=500 > 100)")
	}
}

func TestVerify_ErrorRateFailed(t *testing.T) {
	v := New(Config{MinConsumers: 1, MaxQueueDepth: 1000, MaxErrorRate: 0.01})
	res := v.Verify(Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.05})
	if res.Passed {
		t.Fatal("Verify() passed, want failed (error_rate=0.05 > 0.01)")
	}
}

// scriptedGatherer 按脚本依次返回指标或错误。
type scriptedGatherer struct {
	metrics []Metrics
	errs    []error
	calls   int
}

func (g *scriptedGatherer) Gather(_ context.Context, _ string) (Metrics, error) {
	i := g.calls
	g.calls++
	if i < len(g.errs) && g.errs[i] != nil {
		return Metrics{}, g.errs[i]
	}
	if i < len(g.metrics) {
		return g.metrics[i], nil
	}
	return Metrics{}, errors.New("script exhausted")
}

var okMetrics = Metrics{ConsumerCount: 5, QueueDepth: 100, ErrorRate: 0.001}
var badMetrics = Metrics{ConsumerCount: 0, QueueDepth: 5000, ErrorRate: 0.05}

// TestVerifySequence_RequiresConsecutiveSuccesses 第 N 次连续通过后才算成功。
func TestVerifySequence_RequiresConsecutiveSuccesses(t *testing.T) {
	g := &scriptedGatherer{metrics: []Metrics{okMetrics, okMetrics, okMetrics}}
	v := New(DefaultConfig())

	res, err := v.VerifySequence(context.Background(), g, "inc_1", RunConfig{Attempts: 3, RequiredSuccesses: 3})
	if err != nil {
		t.Fatalf("VerifySequence() error = %v", err)
	}
	if !res.Passed || res.Successes != 3 {
		t.Fatalf("result = %+v, want passed with 3 consecutive successes", res)
	}
	if g.calls != 3 {
		t.Fatalf("gather calls = %d, want 3", g.calls)
	}
}

// TestVerifySequence_MidFailureResetsStreak 中途失败会重置连续计数，即使总成功数够也不通过。
func TestVerifySequence_MidFailureResetsStreak(t *testing.T) {
	g := &scriptedGatherer{metrics: []Metrics{okMetrics, okMetrics, badMetrics, okMetrics}}
	v := New(DefaultConfig())

	res, err := v.VerifySequence(context.Background(), g, "inc_1", RunConfig{Attempts: 4, RequiredSuccesses: 3})
	if err != nil {
		t.Fatalf("VerifySequence() error = %v", err)
	}
	if res.Passed {
		t.Fatalf("result = %+v, want NOT passed (streak reset by third attempt)", res)
	}
	if res.Successes != 1 {
		t.Fatalf("successes = %d, want 1 (streak reset)", res.Successes)
	}
	if res.Reason == "" {
		t.Fatal("reason should be set on failure")
	}
}

// TestVerifySequence_GatherErrorDoesNotPass 采集错误不能按 0 通过，也不能 RESOLVED。
func TestVerifySequence_GatherErrorDoesNotPass(t *testing.T) {
	g := &scriptedGatherer{
		errs:    []error{errors.New("prometheus query returned no data"), errors.New("boom")},
		metrics: []Metrics{okMetrics, okMetrics},
	}
	v := New(DefaultConfig())

	res, err := v.VerifySequence(context.Background(), g, "inc_1", RunConfig{Attempts: 2, RequiredSuccesses: 1})
	if err != nil {
		t.Fatalf("VerifySequence() error = %v", err)
	}
	if res.Passed {
		t.Fatalf("result = %+v, want NOT passed when gathering fails", res)
	}
	for _, a := range res.Attempts {
		if a.Error == "" {
			t.Fatalf("attempt %d should carry the gather error: %+v", a.Attempt, a)
		}
	}
}

// TestVerifySequence_ContextCanceled ctx 取消立即返回错误（调用方不得据此宣称成功/失败）。
func TestVerifySequence_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	g := &scriptedGatherer{metrics: []Metrics{okMetrics}}
	v := New(DefaultConfig())
	if _, err := v.VerifySequence(ctx, g, "inc_1", RunConfig{Attempts: 3, RequiredSuccesses: 3}); err == nil {
		t.Fatal("VerifySequence() with canceled ctx should return an error")
	}
	if g.calls != 0 {
		t.Fatalf("gather calls = %d, want 0 after cancellation", g.calls)
	}
}

// TestVerifySequence_IntervalStopsOnCancel 等待间隔期间取消也要立即停止。
func TestVerifySequence_IntervalStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	g := &scriptedGatherer{metrics: []Metrics{okMetrics, okMetrics, okMetrics, okMetrics}}
	v := New(DefaultConfig())

	res, err := v.VerifySequence(ctx, g, "inc_1", RunConfig{Attempts: 4, RequiredSuccesses: 4, Interval: time.Second})
	if err == nil {
		t.Fatalf("VerifySequence() should return ctx error, got %+v", res)
	}
	if res.Passed {
		t.Fatal("result must not be marked passed when verification was canceled")
	}
}

// TestVerifySequence_RequiredExceedsAttemptsDoesNotLowerThreshold
// required_successes > attempts 属于配置错误：不能被静默降级成"验证一次就通过"。
func TestVerifySequence_RequiredExceedsAttemptsDoesNotLowerThreshold(t *testing.T) {
	g := &scriptedGatherer{metrics: []Metrics{okMetrics, okMetrics}}
	v := New(DefaultConfig())

	res, err := v.VerifySequence(context.Background(), g, "inc_1", RunConfig{Attempts: 1, RequiredSuccesses: 3})
	if err != nil {
		t.Fatalf("VerifySequence() error = %v", err)
	}
	if res.Passed {
		t.Fatalf("result = %+v, must NOT pass: 1 attempt cannot satisfy 3 consecutive successes", res)
	}
	if !strings.Contains(res.Reason, "need 3") {
		t.Fatalf("reason = %q, want it to name the unmet required successes", res.Reason)
	}
	if g.calls != 1 {
		t.Fatalf("gather calls = %d, want 1 (attempts respected)", g.calls)
	}
}

// TestVerifySequence_NilGatherer 未配置采集器时必须报错而不是判成通过。
func TestVerifySequence_NilGatherer(t *testing.T) {
	v := New(DefaultConfig())
	if _, err := v.VerifySequence(context.Background(), nil, "inc_1", DefaultRunConfig()); err == nil {
		t.Fatal("VerifySequence() with nil gatherer should fail")
	}
}
