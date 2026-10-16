---
doc_id: runbook.postgresql.disk_space_exhaustion
doc_type: runbook
service: generic
component: postgresql
incident_type: disk_space_exhaustion
product_version: "PostgreSQL 14-18"
severity: P1
source_kind: official
source_url: https://www.postgresql.org/docs/16/disk-full.html
last_verified_at: 2026-10-16
tags: [postgresql, disk, wal, tablespace, storage]
---
# PostgreSQL 磁盘空间不足

## 触发条件

- 数据盘、WAL 盘或 tablespace 可用空间低于阈值。
- 写入失败、检查点异常或数据库因 WAL 盘满而退出。

## 用户影响

- 写入和维护任务失败，严重时数据库停止服务。

## 快速确认

1. 检查数据库主机各文件系统容量、inode 和挂载状态。
2. 使用数据库大小函数定位增长最快的数据库、表和索引。
3. 检查 WAL、复制槽、归档失败、临时文件和日志增长。

## 诊断步骤

1. 区分数据、WAL、临时文件、归档和日志占用。
2. 检查异常批量写入、索引创建和长事务。
3. 检查复制延迟或失效复制槽是否阻止 WAL 清理。

## 常见原因

- 数据或索引增长超出容量计划。
- 复制槽、归档失败或备库延迟导致 WAL 堆积。
- 临时文件、大查询或日志异常增长。
- 文件系统配额或挂载异常。

## 安全缓解

- 优先扩容或清理已确认可删除的非数据库文件；停止非关键写入和维护任务。
- 不要手工删除 PostgreSQL 数据目录或 `pg_wal` 中的文件。

## 恢复验证

- 文件系统恢复安全余量，写入、检查点、归档和复制正常。

## 回滚与升级

- 新任务导致异常增长时停止并回滚；WAL 盘接近耗尽时立即升级数据库负责人。

## 适用限制

- 数据库大小函数不能替代操作系统文件系统监控。
