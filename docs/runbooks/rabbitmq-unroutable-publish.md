---
doc_id: runbook.rabbitmq.unroutable_publish
doc_type: runbook
service: generic
component: rabbitmq
incident_type: unroutable_publish
product_version: "RabbitMQ 4.3"
severity: P1
source_kind: official
source_url: https://www.rabbitmq.com/docs/publishers
last_verified_at: 2026-10-16
tags: [rabbitmq, publisher, routing, mandatory, confirm]
---
# RabbitMQ 消息无法路由

## 触发条件

- unroutable returned/dropped 指标上升，或 mandatory 发布收到 return。
- publisher confirm 成功，但目标队列没有消息。

## 用户影响

- 业务事件可能没有进入任何队列，消费者无法处理。

## 快速确认

1. 核对 exchange、routing key、vhost 和目标绑定。
2. 检查 unroutable 指标、客户端 return 回调和 broker 日志。
3. 确认目标队列是否存在且绑定与发布端预期一致。

## 诊断步骤

1. 检查发布端与消费端配置是否来自同一环境。
2. 比较最近拓扑变更、队列声明和绑定参数。
3. 区分“broker 接收成功”和“消息成功路由”。

## 常见原因

- routing key 或 exchange 配置错误。
- 目标绑定缺失，或队列被删除后未恢复。
- 发布到错误的 vhost。
- 非 mandatory 发布使无法路由的消息被丢弃。

## 安全缓解

- 恢复正确绑定或回滚发布配置；只有确认业务幂等后才重放受影响消息。

## 恢复验证

- unroutable 指标停止增长，目标队列收到测试消息，消费者完成确认。

## 回滚与升级

- 新拓扑导致故障时恢复原绑定；无法确认丢失范围时升级业务负责人核对数据。

## 适用限制

- publisher confirm 只确认发布方与 broker 的交互，不等价于路由或消费成功。
