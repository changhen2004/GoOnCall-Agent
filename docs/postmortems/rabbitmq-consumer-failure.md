---
service: GoCommunity
severity: P1
tags: [rabbitmq, worker, postmortem]
---
# Incident INC-20260829-001

## Summary

GoCommunity RabbitMQ consumer 数量下降导致消息积压。

## Impact

Queue depth 从 120 上升到 2418。

## Root Cause

Worker 与 RabbitMQ 的连接异常。

## Evidence

- Consumer count: 8 -> 2
- Queue depth: 120 -> 2418
- Error log: connection refused

## Resolution

Restart worker deployment.

## Prevention

- 增加 consumer count 告警
- 增加 worker readiness 检查
- 发布后增加自动验证
