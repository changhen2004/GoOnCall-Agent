---
service: GoCommunity
tags: [architecture]
---
# GoCommunity 架构概览

GoCommunity 是一个社区服务，包含：

- Gin HTTP API
- RabbitMQ Worker（异步任务消费）
- Redis 缓存
- PostgreSQL 持久化
- Prometheus / Grafana 监控

Worker 通过 RabbitMQ 消费消息，consumer count 下降会导致消息积压。
