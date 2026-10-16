---
doc_id: runbook.rabbitmq.consumer_count_dropped
doc_type: runbook
service: generic
component: rabbitmq
incident_type: consumer_count_dropped
product_version: "RabbitMQ 4.3"
severity: P1
source_kind: official
source_url: https://www.rabbitmq.com/docs/consumers
last_verified_at: 2026-10-16
tags: [rabbitmq, consumer, worker, connection]
---
# RabbitMQ 消费者数量下降

## 触发条件

- 队列 consumer 数量低于期望副本数或降为零。
- 同时出现消息积压、消费速率下降或应用重连日志。

## 用户影响

- 消息停止或延迟处理，异步业务可能无法完成。

## 快速确认

1. 在队列指标或管理界面确认受影响的队列、vhost 和 consumer 数量。
2. 检查消费者部署的健康状态、重启次数和最近发布记录。
3. 检查 RabbitMQ 连接、认证和权限错误。

## 诊断步骤

1. 区分消费者进程退出、网络断开、认证失败和订阅未成功。
2. 检查客户端是否进入自动恢复循环，以及拓扑恢复是否成功。
3. 确认队列名称、vhost、路由配置与部署环境一致。

## 常见原因

- Worker 崩溃、被驱逐或启动失败。
- 网络中断、心跳超时或凭证失效。
- 队列被删除后客户端没有恢复订阅。
- 发布变更修改了 vhost、队列名或权限。

## 安全缓解

- 优先恢复失败实例或回滚最近部署；不要在未确认幂等性时重复启动大量消费者。

## 恢复验证

- consumer 数量恢复并保持稳定，消息确认速率恢复，积压开始下降。

## 回滚与升级

- 新版本恢复订阅失败时回滚部署；疑似 broker 集群问题时升级给 RabbitMQ 管理员。

## 适用限制

- consumer 数量正常不代表消费逻辑正常，还需检查确认速率和处理错误。
