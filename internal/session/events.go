package session

import (
	"sync"
	"time"
)

// Event 是推送给 SSE 订阅者的实时事件。它不承担补播或持久化职责。
type Event struct {
	Type       string    `json:"type"`
	RunID      string    `json:"run_id"`
	Delta      string    `json:"delta,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	Arguments  string    `json:"arguments,omitempty"`
	ApprovalID string    `json:"approval_id,omitempty"`
	Status     string    `json:"status,omitempty"`
	Result     string    `json:"result,omitempty"`
	Error      string    `json:"error,omitempty"`
	At         time.Time `json:"at"`
}

type subscriber struct {
	ch   chan Event
	once sync.Once
}

func (s *subscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

// Subscribe 订阅一次运行的实时事件。返回取消函数，调用方可随时退订。
//
// 每个订阅者有固定大小缓冲；缓冲满时直接断开该订阅，避免阻塞执行，
// 客户端可改用查询接口获取已保存状态。
func (s *Service) Subscribe(runID string) (<-chan Event, func()) {
	sub := &subscriber{ch: make(chan Event, s.opts.SubscriberBuffer)}

	s.mu.Lock()
	if s.subs[runID] == nil {
		s.subs[runID] = make(map[*subscriber]struct{})
	}
	s.subs[runID][sub] = struct{}{}
	s.mu.Unlock()

	return sub.ch, func() { s.dropSubscriber(runID, sub) }
}

func (s *Service) emit(runID string, event Event) {
	event.RunID = runID
	if event.At.IsZero() {
		event.At = time.Now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	group := s.subs[runID]
	for sub := range group {
		select {
		case sub.ch <- event:
		default:
			delete(group, sub)
			sub.close()
		}
	}
	if len(group) == 0 {
		delete(s.subs, runID)
	}
}

func (s *Service) dropSubscriber(runID string, sub *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()

	group := s.subs[runID]
	if _, ok := group[sub]; !ok {
		return
	}
	delete(group, sub)
	if len(group) == 0 {
		delete(s.subs, runID)
	}
	sub.close()
}

func (s *Service) closeSubscribers(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for sub := range s.subs[runID] {
		sub.close()
	}
	delete(s.subs, runID)
}
