---
doc_id: runbook.kubernetes.pod_pending
doc_type: runbook
service: generic
component: kubernetes
incident_type: pod_pending
product_version: "Kubernetes current"
severity: P2
source_kind: official
source_url: https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/
last_verified_at: 2026-10-16
tags: [kubernetes, pod, pending, scheduler]
---
# Kubernetes Pod 长时间 Pending

## 触发条件

- Pod 持续处于 `Pending`，期望副本无法调度或启动。

## 用户影响

- 扩容或发布无法完成，服务容量可能不足。

## 快速确认

1. 查看 Pod describe 输出末尾的调度事件。
2. 检查资源请求、节点可分配资源、污点和亲和规则。
3. 若已调度，检查卷挂载和镜像拉取事件。

## 诊断步骤

1. 区分 `FailedScheduling`、卷绑定等待和镜像问题。
2. 检查 namespace 配额、LimitRange 和 PodDisruptionBudget 的关联影响。
3. 对比相同工作负载的正常 Pod 规格。

## 常见原因

- CPU、内存或扩展资源不足。
- nodeSelector、亲和性或污点容忍不匹配。
- PVC 无法绑定或可用区不匹配。
- namespace 配额耗尽。

## 安全缓解

- 经容量评估后扩容节点或调整不合理的请求；通过发布流程修正调度约束。

## 恢复验证

- Pod 被调度并进入 Ready，Deployment 可用副本达到期望值。

## 回滚与升级

- 新规格无法调度时回滚；涉及节点池和存储时升级平台负责人。

## 适用限制

- Pending 也可能发生在已调度后的容器准备阶段，不能只检查调度器。
