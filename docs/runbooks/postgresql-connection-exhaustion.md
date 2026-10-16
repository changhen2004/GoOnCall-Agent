---
doc_id: runbook.postgresql.connection_exhaustion
doc_type: runbook
service: generic
component: postgresql
incident_type: connection_exhaustion
product_version: "PostgreSQL 18"
severity: P1
source_kind: official
source_url: https://www.postgresql.org/docs/current/runtime-config-connection.html
last_verified_at: 2026-10-16
tags: [postgresql, connection, max_connections, pool]
---
# PostgreSQL 连接耗尽

## 触发条件

- 客户端出现 remaining connection slots、too many clients 或连接池超时。
- 活跃与空闲连接数接近 `max_connections`。

## 用户影响

- 新请求无法获得数据库连接，接口错误率和延迟上升。

## 快速确认

1. 查询 `pg_stat_activity`，按数据库、用户、应用和状态统计连接。
2. 检查 `max_connections`、保留连接槽和连接池上限。
3. 检查是否存在大量 idle in transaction 或连接突增的应用实例。

## 诊断步骤

1. 对比应用副本数、每实例连接池大小与数据库上限。
2. 检查连接泄漏、长事务和异常重试风暴。
3. 确认数据库 CPU、内存和锁等待是否同时异常。

## 常见原因

- 应用连接未归还或池参数过大。
- 扩容后总连接预算超过数据库上限。
- 长事务、锁等待或数据库变慢占住连接。
- 故障重试造成连接风暴。

## 安全缓解

- 先限制异常调用方和重试并恢复合理的池上限；终止会话必须确认业务影响并走审批。
- 不要只提高 `max_connections`，它会增加数据库资源占用。

## 恢复验证

- 连接使用率回到安全范围，获取连接延迟与应用错误率恢复。

## 回滚与升级

- 最近连接池或副本变更导致故障时回滚；需要终止未知事务时升级数据库负责人。

## 适用限制

- 连接多不一定代表耗尽，应同时确认剩余连接槽和业务错误。
