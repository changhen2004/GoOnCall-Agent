---
doc_id: runbook.postgresql.replication_lag
doc_type: runbook
service: generic
component: postgresql
incident_type: replication_lag
product_version: "PostgreSQL 18"
severity: P1
source_kind: official
source_url: https://www.postgresql.org/docs/current/warm-standby.html
last_verified_at: 2026-10-16
tags: [postgresql, replication, wal, lag, standby]
---
# PostgreSQL 复制延迟

## 触发条件

- 主库与备库 WAL 位置或 replay 时间差持续扩大。
- 只读请求读到旧数据，或 WAL 保留量持续增长。

## 用户影响

- 备库数据陈旧，故障切换的 RPO/RTO 风险增加，主库磁盘可能承压。

## 快速确认

1. 在主库检查 `pg_stat_replication` 的 state、sent/write/flush/replay 位置。
2. 在备库检查 WAL receiver、replay 状态和最近重放时间。
3. 检查网络、磁盘 I/O、长查询和恢复冲突。

## 诊断步骤

1. 区分 WAL 发送、传输、写入、刷新和重放阶段的延迟。
2. 检查复制槽 WAL 积压和主库可用磁盘。
3. 检查备库 CPU、I/O 与长时间只读查询。

## 常见原因

- 主备网络抖动或带宽不足。
- 备库磁盘或 CPU 饱和。
- 长查询阻碍清理或重放。
- 复制进程断开、槽配置异常。

## 安全缓解

- 降低备库非关键查询和主库非关键写入；故障切换前必须确认数据差距和业务容忍度。

## 恢复验证

- 各阶段 WAL 差距持续缩小并稳定，复制进程正常，主库 WAL 空间恢复。

## 回滚与升级

- 新配置导致延迟时回滚；延迟超过 RPO 或主库磁盘告急时立即升级。

## 适用限制

- 时间延迟在低写入期可能为空或不稳定，应同时比较 WAL 位置。
