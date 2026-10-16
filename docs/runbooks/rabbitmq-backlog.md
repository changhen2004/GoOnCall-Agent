---
doc_id: runbook.rabbitmq.queue_backlog
doc_type: runbook
service: generic
component: rabbitmq
incident_type: queue_backlog
product_version: "RabbitMQ 4.3"
severity: P2
source_kind: official
source_url: https://www.rabbitmq.com/docs/monitoring
last_verified_at: 2026-10-16
tags: [rabbitmq, queue, backlog, consumer]
---
# RabbitMQ 队列积压

## 触发条件

- `messages_ready` 或队列深度持续上升，而消费速率低于发布速率。
- 消费端到端延迟或业务异步任务延迟超过告警阈值。

## 用户影响

- 异步任务完成变慢，严重时会因磁盘或内存压力影响发布。

## 快速确认

1. 对比目标队列的 ready、unacknowledged、publish 与 deliver/ack 速率。
2. 检查 consumer 数量是否下降，以及消费者是否仍在确认消息。
3. 结合应用指标确认是发布突增还是消费能力下降。

## 诊断步骤

1. 检查消费者日志中的超时、连接恢复、处理失败和重复重入。
2. 检查下游数据库、HTTP 服务等依赖是否变慢。
3. 检查预取值、单条处理耗时和消息大小是否近期变化。

## 常见原因

- 消费实例退出或无法连接 RabbitMQ。
- 下游依赖变慢，消费者处理时间上升。
- 发布流量突增，当前并发不足。
- 消费失败后持续重入队。

## 安全缓解

- 先修复依赖或恢复异常消费者；确认处理具备幂等性后再扩容消费者。
- 必要时限制非关键发布流量，避免积压继续扩大。

## 恢复验证

- ready 消息连续下降，deliver/ack 速率不低于 publish 速率。
- consumer 数量稳定，应用错误率与处理延迟恢复。

## 回滚与升级

- 扩容导致下游过载时恢复原副本数；无法在值班权限内缓解时升级给消息队列与业务负责人。

## 适用限制

- 队列深度只能证明发生积压，不能单独证明消费者是根因。
