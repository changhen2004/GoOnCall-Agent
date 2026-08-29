package verifier

import "testing"

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
