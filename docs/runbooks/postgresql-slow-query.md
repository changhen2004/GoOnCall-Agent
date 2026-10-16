---
doc_id: runbook.postgresql.slow_query
doc_type: runbook
service: generic
component: postgresql
incident_type: slow_query
product_version: "PostgreSQL 18"
severity: P2
source_kind: official
source_url: https://www.postgresql.org/docs/current/using-explain.html
last_verified_at: 2026-10-16
tags: [postgresql, query, latency, explain]
---
# PostgreSQL 慢查询

## 触发条件

- 查询 P95/P99 或接口延迟升高，数据库活动中出现长时间运行 SQL。

## 用户影响

- 请求超时、连接池占满，并可能拖慢同库其他业务。

## 快速确认

1. 从 `pg_stat_activity` 确认活动查询、运行时间和等待事件。
2. 使用监控或 `pg_stat_statements` 确认高总耗时与高调用量查询。
3. 检查 CPU、I/O、缓存命中、锁等待和数据量变化。

## 诊断步骤

1. 使用 `EXPLAIN` 查看计划；`EXPLAIN ANALYZE` 会实际执行语句，只能在确认安全后使用。
2. 比较估算行数与实际数据分布，检查统计信息和索引使用。
3. 区分单条 SQL 回归、锁等待和系统资源饱和。

## 常见原因

- 缺失或不合适的索引。
- 统计信息过旧导致计划错误。
- 数据量增长、参数分布变化或返回行数过大。
- 锁等待、磁盘 I/O 或缓存命中下降。

## 安全缓解

- 限制异常流量、回滚引入慢 SQL 的版本；索引和参数变更应先验证写入与存储成本。

## 恢复验证

- 查询及接口延迟恢复，数据库 CPU/I/O 和连接占用回落。

## 回滚与升级

- 新索引或配置导致写入退化时回滚；涉及执行计划不稳定时升级数据库负责人。

## 适用限制

- 只看单次执行时间可能忽略高频短查询造成的总负载。
