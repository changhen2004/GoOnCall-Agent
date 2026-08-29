package runtime

import "sync"

// StreamEvent 是一条 SSE 事件。
type StreamEvent struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// StreamBroker 是事件发布订阅器，作为 SSE 流式输出的后端。
type StreamBroker struct {
	mu      sync.RWMutex
	history map[string][]StreamEvent
	subs    map[string][]chan StreamEvent
}

// NewStreamBroker 创建事件发布订阅器。
func NewStreamBroker() *StreamBroker {
	return &StreamBroker{
		history: make(map[string][]StreamEvent),
		subs:    make(map[string][]chan StreamEvent),
	}
}

// Publish 发布事件到指定 run，写入历史并推送给订阅者。
func (b *StreamBroker) Publish(runID string, ev StreamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.history[runID] = append(b.history[runID], ev)
	for _, ch := range b.subs[runID] {
		select {
		case ch <- ev:
		default:
			// 订阅者缓冲区满则丢弃，避免阻塞发布方。
		}
	}
}

// Subscribe 订阅 run 的事件流：先回放历史，再接收实时事件。返回取消函数。
func (b *StreamBroker) Subscribe(runID string) (<-chan StreamEvent, func()) {
	ch := make(chan StreamEvent, 256)

	b.mu.Lock()
	for _, ev := range b.history[runID] {
		ch <- ev
	}
	b.subs[runID] = append(b.subs[runID], ch)
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			subs := b.subs[runID]
			for i, c := range subs {
				if c == ch {
					b.subs[runID] = append(subs[:i], subs[i+1:]...)
					break
				}
			}
			close(ch)
			b.mu.Unlock()
		})
	}
	return ch, cancel
}
