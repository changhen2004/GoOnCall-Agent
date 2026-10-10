package messaging

import (
	"context"
	"errors"
	"reflect"
	"testing"

	clientmodel "github.com/prometheus/client_model/go"
	amqp "github.com/rabbitmq/amqp091-go"

	"gooncall-agent/internal/observability/metrics"
)

type recordingAcknowledger struct {
	events *[]string
}

func (a *recordingAcknowledger) Ack(uint64, bool) error {
	*a.events = append(*a.events, "ack")
	return nil
}

func (*recordingAcknowledger) Nack(uint64, bool, bool) error { return nil }
func (*recordingAcknowledger) Reject(uint64, bool) error     { return nil }

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

// TestFailedMessageRoute 重试未耗尽时只加计数；重试耗尽时标记进 DLQ。
func TestFailedMessageRoute(t *testing.T) {
	key, headers, toDLQ := failedMessageRoute(amqp.Table{})
	if key != retryRoutingKey || toDLQ {
		t.Fatalf("route(%v) = %s toDLQ=%v, want %s false", nil, key, toDLQ, retryRoutingKey)
	}
	if n := retryCount(headers); n != 1 {
		t.Fatalf("retry count = %d, want 1", n)
	}

	key, headers, toDLQ = failedMessageRoute(amqp.Table{RetryCountHeader: int32(MaxRetries)})
	if key != dlqRoutingKey || !toDLQ {
		t.Fatalf("route(max retries) = %s toDLQ=%v, want %s true", key, toDLQ, dlqRoutingKey)
	}
	if n := retryCount(headers); n != MaxRetries {
		t.Fatalf("retry count = %d, want %d (DLQ keeps the last count)", n, MaxRetries)
	}
}

func TestRecordRoutedMessageCountsConfirmedDestination(t *testing.T) {
	counterValue := func(counter interface {
		Write(*clientmodel.Metric) error
	}) float64 {
		metric := &clientmodel.Metric{}
		if err := counter.Write(metric); err != nil {
			t.Fatalf("write counter: %v", err)
		}
		return metric.GetCounter().GetValue()
	}
	retriesBefore := counterValue(metrics.RetriesTotal)
	dlqBefore := counterValue(metrics.DLQTotal)

	recordRoutedMessage(false)
	recordRoutedMessage(true)

	if got := counterValue(metrics.RetriesTotal) - retriesBefore; got != 1 {
		t.Fatalf("retry delta = %v, want 1", got)
	}
	if got := counterValue(metrics.DLQTotal) - dlqBefore; got != 1 {
		t.Fatalf("dlq delta = %v, want 1", got)
	}
}

// TestOnDeadLetter 终态回调只在 DLQ 确认后被调用；回调失败只记录，不影响死信。
func TestOnDeadLetter(t *testing.T) {
	ctx := context.Background()
	cause := errors.New("diagnosis failed")

	var got []Event
	var gotCause []error
	c := NewConsumer(nil, "gooncall.agent.queue").WithTerminalHandler(func(_ context.Context, e Event, err error) error {
		got = append(got, e)
		gotCause = append(gotCause, err)
		return nil
	})
	c.onDeadLetter(ctx, Event{ID: "evt_1", Type: AgentRequested}, cause)
	if len(got) != 1 || got[0].ID != "evt_1" {
		t.Fatalf("terminal callback calls = %+v, want exactly one for evt_1", got)
	}
	if len(gotCause) != 1 || gotCause[0] != cause {
		t.Fatalf("terminal callback cause = %+v, want the handler error", gotCause)
	}

	// 回调失败：只记录日志，不 panic 也不阻塞死信。
	failing := NewConsumer(nil, "gooncall.agent.queue").WithTerminalHandler(func(context.Context, Event, error) error {
		return errors.New("incident finalization failed")
	})
	failing.onDeadLetter(ctx, Event{ID: "evt_2"}, cause)

	// 未配置回调：什么都不做。
	NewConsumer(nil, "gooncall.agent.queue").onDeadLetter(ctx, Event{ID: "evt_3"}, cause)
}

// TestFinishDeadLetter_CallbackBeforeAckAndSurvivesCancellation 锁定 DLQ 收尾顺序：
// Worker context 即使已取消，也必须先完成终态回调，再 Ack 原消息。
func TestFinishDeadLetter_CallbackBeforeAckAndSurvivesCancellation(t *testing.T) {
	events := make([]string, 0, 2)
	c := NewConsumer(nil, "gooncall.agent.queue").WithTerminalHandler(func(ctx context.Context, _ Event, _ error) error {
		if err := ctx.Err(); err != nil {
			t.Fatalf("terminal context = %v, want active context", err)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("terminal context must have a deadline")
		}
		events = append(events, "terminal")
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg := amqp.Delivery{Acknowledger: &recordingAcknowledger{events: &events}, DeliveryTag: 1}

	c.finishDeadLetter(ctx, msg, Event{ID: "evt_1"}, errors.New("boom"))

	if want := []string{"terminal", "ack"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
