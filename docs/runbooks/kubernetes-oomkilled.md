---
doc_id: runbook.kubernetes.oomkilled
doc_type: runbook
service: generic
component: kubernetes
incident_type: oomkilled
product_version: "Kubernetes current"
severity: P1
source_kind: official
source_url: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
last_verified_at: 2026-10-16
tags: [kubernetes, pod, oomkilled, memory]
---
# Kubernetes 容器 OOMKilled

## 触发条件

- 容器上次终止原因为 `OOMKilled`，并伴随重启或不可用。

## 用户影响

- 请求中断、任务失败或 Pod 反复重启。

## 快速确认

1. 查看容器终止原因、内存 request/limit 和重启次数。
2. 对比工作集内存、节点内存压力与最近流量。
3. 检查是否发生新版本、缓存增长或数据规模变化。

## 诊断步骤

1. 区分容器超过 limit 与节点级内存压力。
2. 检查内存是否持续增长、瞬时峰值或输入相关。
3. 检查 heap、缓存、并发和大对象处理路径。

## 常见原因

- limit 低于正常峰值。
- 应用内存泄漏或无界缓存。
- 并发或请求体大小突增。
- 节点整体内存不足。

## 安全缓解

- 经容量验证后临时提高 limit 或降低并发；同时保留内存证据，不能只靠重启结束调查。

## 恢复验证

- 容器不再 OOM，内存曲线稳定，重启次数和错误率停止增长。

## 回滚与升级

- 新版本出现内存回归时回滚；持续增长需升级开发负责人定位泄漏。

## 适用限制

- 内存 limit 是反应式强制限制，单次 OOM 不足以判断泄漏。
