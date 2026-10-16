---
doc_id: runbook.rabbitmq.connection_failure
doc_type: runbook
service: generic
component: rabbitmq
incident_type: connection_failure
product_version: "RabbitMQ 4.3"
severity: P1
source_kind: official
source_url: https://www.rabbitmq.com/docs/troubleshooting-networking
last_verified_at: 2026-10-16
tags: [rabbitmq, connection, heartbeat, tls, authentication]
---
# RabbitMQ 连接失败

## 触发条件

- 客户端出现 connection refused、握手失败、认证失败或心跳超时。
- 连接数下降，发布或消费停止。

## 用户影响

- 消息无法发布或消费；未确认的操作结果可能未知。

## 快速确认

1. 检查目标主机、端口、DNS 和网络策略是否正确。
2. 检查 RabbitMQ 节点、监听端口和资源告警状态。
3. 对齐客户端与服务端日志中的时间、连接名和错误原因。

## 诊断步骤

1. 区分 TCP 不可达、TLS 握手、认证授权和 AMQP 协议异常。
2. 检查凭证、vhost 权限和证书有效期。
3. 检查心跳配置、网络丢包与连接频繁创建销毁。

## 常见原因

- 地址、端口或 DNS 配置错误。
- 网络策略、防火墙或负载均衡阻断。
- 凭证、vhost 权限或 TLS 证书错误。
- 节点资源告警或客户端恢复逻辑失败。

## 安全缓解

- 修复配置或网络后让客户端按退避策略恢复；发布结果未知的消息只能在业务幂等条件成立时重试。

## 恢复验证

- 连接稳定、认证成功，发布确认和消费确认恢复，无持续重连。

## 回滚与升级

- 最近配置或证书变更导致故障时回滚；节点不可用时按集群运行手册升级处理。

## 适用限制

- 仅有 publisher confirm 超时不能证明消息未路由到队列。
