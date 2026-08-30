package messaging

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRetryCount(t *testing.T) {
	cases := []struct {
		name    string
		headers amqp.Table
		want    int
	}{
		{"no header", nil, 0},
		{"empty table", amqp.Table{}, 0},
		{"int32", amqp.Table{RetryCountHeader: int32(2)}, 2},
		{"int64", amqp.Table{RetryCountHeader: int64(3)}, 3},
		{"int", amqp.Table{RetryCountHeader: 1}, 1},
		{"float64", amqp.Table{RetryCountHeader: float64(2)}, 2},
		{"wrong type", amqp.Table{RetryCountHeader: "2"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryCount(tc.headers); got != tc.want {
				t.Fatalf("retryCount() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRetryDecision(t *testing.T) {
	cases := []struct {
		retries   int
		wantKey   string
		wantNext  int
		wantRetry bool
	}{
		{0, retryRoutingKey, 1, true},
		{1, retryRoutingKey, 2, true},
		{2, retryRoutingKey, 3, true},
		{3, dlqRoutingKey, 3, false},
		{5, dlqRoutingKey, 5, false},
	}
	for _, tc := range cases {
		key, next, toRetry := retryDecision(tc.retries)
		if key != tc.wantKey || next != tc.wantNext || toRetry != tc.wantRetry {
			t.Fatalf("retryDecision(%d) = (%s,%d,%v), want (%s,%d,%v)",
				tc.retries, key, next, toRetry, tc.wantKey, tc.wantNext, tc.wantRetry)
		}
	}
}

func TestCloneHeaders(t *testing.T) {
	src := amqp.Table{RetryCountHeader: int32(1), "k": "v"}
	dup := cloneHeaders(src)
	dup[RetryCountHeader] = int32(9)
	if src[RetryCountHeader] != int32(1) {
		t.Fatal("cloneHeaders() mutated source")
	}
	if dup["k"] != "v" {
		t.Fatal("cloneHeaders() lost keys")
	}
}

func TestQueueNames(t *testing.T) {
	c := NewConsumer(nil, "gooncall.agent.queue")
	if got := c.retryQueueName(); got != "gooncall.agent.queue.retry" {
		t.Fatalf("retryQueueName() = %s, want gooncall.agent.queue.retry", got)
	}
	if got := c.dlqQueueName(); got != "gooncall.agent.queue.dlq" {
		t.Fatalf("dlqQueueName() = %s, want gooncall.agent.queue.dlq", got)
	}
}
