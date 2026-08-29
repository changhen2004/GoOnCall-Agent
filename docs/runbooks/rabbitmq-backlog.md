---
service: GoCommunity
severity: P1
tags: [rabbitmq, worker]
---
# 消费者异常（RabbitMQ Backlog）

## 症状

- RabbitMQ Queue depth 持续上升
- Consumer count 下降
- 消息大量积压

## 排查步骤

1. 检查 RabbitMQ 队列的 consumer count 是否下降。
2. 检查 worker 日志是否出现 connection refused。
3. 检查最近是否有新版本发布。

## 处理方式

- 重启 worker 部署以恢复消费者。
- 验证 consumer count 恢复、queue depth 下降。
- 验证 HTTP P95 延迟恢复正常。
