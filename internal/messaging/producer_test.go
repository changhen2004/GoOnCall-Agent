package messaging

import (
	"context"
	"testing"
)

// TestProducer_PublishNilConn 验证 nil 连接时不 panic，而是返回错误。
// （publisher confirm 全流程依赖真实 broker，由集成环境验证。）
func TestProducer_PublishNilConn(t *testing.T) {
	p := &Producer{} // conn == nil
	err := p.Publish(context.Background(), AgentRequested, Event{})
	if err == nil {
		t.Fatal("Publish() with nil conn should return error, not panic")
	}
}

func TestPublishErrors(t *testing.T) {
	if ErrPublishNotConfirmed == nil || ErrPublishUnroutable == nil {
		t.Fatal("publish sentinel errors should be defined")
	}
	if ErrPublishNotConfirmed.Error() == "" || ErrPublishUnroutable.Error() == "" {
		t.Fatal("publish sentinel errors should have messages")
	}
}
