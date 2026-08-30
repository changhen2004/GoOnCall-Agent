package messaging

import (
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// 消费失败重试与死信策略：
//
//	gooncall.agent.queue.retry（TTL 到期后按原路由键重新投递到主队列）
//	        ↑                              |
//	gooncall.agent.queue ------------------+-- 超过 MaxRetries --> gooncall.agent.queue.dlq
//
// 失败消息不再 Nack(requeue=true) 无限循环，而是路由到重试队列；
// 重试次数记录在 x-retry-count 消息头，超过 MaxRetries 后进入 DLQ。
const (
	// RetryCountHeader 记录已重试次数的消息头（x-retry-count）。
	RetryCountHeader = "x-retry-count"
	// MaxRetries 最大重试次数（3 次），超过后进入死信队列。
	MaxRetries = 3
	// RetryTTL 重试队列消息延迟，TTL 到期后重新投递到主队列。
	RetryTTL = 5 * time.Second

	retryRoutingKey = "agent.retry"
	dlqRoutingKey   = "agent.dlq"
)

// retryQueueName 返回重试队列名（主队列名 + ".retry"）。
func (c *Consumer) retryQueueName() string { return c.queue + ".retry" }

// dlqQueueName 返回死信队列名（主队列名 + ".dlq"）。
func (c *Consumer) dlqQueueName() string { return c.queue + ".dlq" }

// retryCount 从消息头读取已重试次数（x-retry-count），缺省为 0。
func retryCount(h amqp.Table) int {
	v, ok := h[RetryCountHeader]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// retryDecision 根据失败次数决定路由目标与下一次重试计数：
// 失败次数 < MaxRetries → 重试队列（重试计数 +1）；否则 → DLQ。
func retryDecision(retries int) (routingKey string, nextRetries int, toRetry bool) {
	if retries < MaxRetries {
		return retryRoutingKey, retries + 1, true
	}
	return dlqRoutingKey, retries, false
}

// cloneHeaders 复制消息头（避免修改原 Delivery 的 Headers）。
func cloneHeaders(h amqp.Table) amqp.Table {
	out := make(amqp.Table, len(h)+1)
	for k, v := range h {
		out[k] = v
	}
	return out
}
