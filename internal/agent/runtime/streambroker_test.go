package runtime

import (
	"testing"
	"time"
)

func TestStreamBroker_PublishAndSubscribe(t *testing.T) {
	b := NewStreamBroker()
	ch, cancel := b.Subscribe("run_1")
	defer cancel()

	b.Publish("run_1", StreamEvent{Type: "tool.started", Data: map[string]any{"tool": "prometheus.query"}})

	select {
	case ev := <-ch:
		if ev.Type != "tool.started" {
			t.Fatalf("type = %s, want tool.started", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestStreamBroker_HistoryReplay(t *testing.T) {
	b := NewStreamBroker()
	b.Publish("run_1", StreamEvent{Type: "run.started", Data: map[string]any{"run_id": "run_1"}})
	b.Publish("run_1", StreamEvent{Type: "done", Data: map[string]any{}})

	ch, cancel := b.Subscribe("run_1")
	defer cancel()

	for i, want := range []string{"run.started", "done"} {
		select {
		case ev := <-ch:
			if ev.Type != want {
				t.Fatalf("event %d type = %s, want %s", i, ev.Type, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

func TestStreamBroker_Cancel(t *testing.T) {
	b := NewStreamBroker()
	ch, cancel := b.Subscribe("run_1")
	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after cancel")
	}
}

func TestStreamBroker_IsolationByRun(t *testing.T) {
	b := NewStreamBroker()
	ch1, _ := b.Subscribe("run_1")
	ch2, _ := b.Subscribe("run_2")

	b.Publish("run_1", StreamEvent{Type: "run.started"})

	select {
	case <-ch1:
		// ok
	case <-time.After(time.Second):
		t.Fatal("run_1 should receive its event")
	}

	select {
	case ev := <-ch2:
		t.Fatalf("run_2 should not receive run_1 event: %v", ev)
	case <-time.After(100 * time.Millisecond):
		// ok
	}
}
