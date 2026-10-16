---
doc_id: runbook.postgresql.lock_contention
doc_type: runbook
service: generic
component: postgresql
incident_type: lock_contention
product_version: "PostgreSQL 18"
severity: P1
source_kind: official
source_url: https://www.postgresql.org/docs/current/monitoring-locks.html
last_verified_at: 2026-10-16
tags: [postgresql, lock, blocking, transaction]
---
# PostgreSQL 锁争用

## 触发条件

- 查询等待时间上升，`wait_event_type` 为 Lock，或出现锁等待告警。

## 用户影响

- 事务阻塞、请求超时，连接可能继续堆积。

## 快速确认

1. 联合 `pg_stat_activity`、`pg_locks` 和 `pg_blocking_pids` 找到等待链。
2. 确认最上游阻塞会话、事务开始时间、应用和 SQL。
3. 检查是否存在 idle in transaction。

## 诊断步骤

1. 区分正常短暂锁、长事务和 DDL 阻塞。
2. 检查阻塞事务是否仍在执行、是否可由业务安全重试。
3. 检查近期发布、迁移和批处理任务。

## 常见原因

- 长事务持锁或忘记提交。
- DDL 与在线业务并发。
- 更新顺序不一致导致死锁或等待链。
- 批处理一次修改过多数据。

## 安全缓解

- 优先停止产生新冲突的任务；取消或终止阻塞会话必须确认事务影响并走审批。

## 恢复验证

- 锁等待链消失，等待会话数、查询延迟和连接占用恢复。

## 回滚与升级

- 迁移导致阻塞时按迁移方案回滚；无法判断事务影响时升级数据库与业务负责人。

## 适用限制

- `pg_locks` 是瞬时视图，应结合查询开始时间和应用上下文判断。
