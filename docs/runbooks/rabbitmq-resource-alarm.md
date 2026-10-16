---
doc_id: runbook.rabbitmq.resource_alarm
doc_type: runbook
service: generic
component: rabbitmq
incident_type: resource_alarm
product_version: "RabbitMQ 4.3"
severity: P1
source_kind: official
source_url: https://www.rabbitmq.com/docs/alarms
last_verified_at: 2026-10-16
tags: [rabbitmq, memory, disk, alarm, blocked]
---
# RabbitMQ 内存或磁盘资源告警

## 触发条件

- RabbitMQ 报告 memory alarm 或 disk alarm。
- 发布连接进入 blocked 状态，写入超时或停止。

## 用户影响

- 集群可能阻塞发布，积压和业务延迟继续扩大。

## 快速确认

1. 使用节点诊断或监控确认告警类型与受影响节点。
2. 检查可用磁盘、RabbitMQ 内存、容器限制和消息积压。
3. 检查是否有异常大消息、队列增长或分页行为变化。

## 诊断步骤

1. 对磁盘告警确认数据盘、WAL/日志盘和 inode 使用率。
2. 对内存告警检查队列类型、未确认消息和单连接内存。
3. 检查近期阈值、资源限制或负载变化。

## 常见原因

- 队列持续积压或消息体积增长。
- 磁盘空间不足、日志未轮转或卷容量异常。
- 容器内存限制与 RabbitMQ 水位不匹配。

## 安全缓解

- 优先释放非 RabbitMQ 的可回收空间或扩容资源；降低非关键发布流量。
- 不要在未评估数据保留要求时删除队列或消息。

## 恢复验证

- 资源告警清除，发布连接解除 blocked，磁盘和内存保持安全余量。

## 回滚与升级

- 调整阈值后指标恶化应恢复原配置；无法安全释放资源时立即升级。

## 适用限制

- 水位是阻塞阈值，不是 RabbitMQ 进程内存的硬上限。
