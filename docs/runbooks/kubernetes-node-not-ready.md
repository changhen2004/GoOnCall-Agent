---
doc_id: runbook.kubernetes.node_not_ready
doc_type: runbook
service: generic
component: kubernetes
incident_type: node_not_ready
product_version: "Kubernetes current"
severity: P1
source_kind: official
source_url: https://kubernetes.io/docs/tasks/debug/debug-cluster/
last_verified_at: 2026-10-16
tags: [kubernetes, node, notready, kubelet]
---
# Kubernetes Node NotReady

## 触发条件

- Node Ready 条件为 False 或 Unknown，并持续超过告警窗口。
- 节点上的 Pod 出现驱逐、不可达或容量下降。

## 用户影响

- 工作负载迁移或不可用，集群剩余节点承压。

## 快速确认

1. 查看节点条件、事件、污点和最近心跳时间。
2. 检查 kubelet、容器运行时、磁盘、内存和网络状态。
3. 确认是单节点还是控制面/网络的集群性问题。

## 诊断步骤

1. 检查 MemoryPressure、DiskPressure、PIDPressure 与 NetworkUnavailable。
2. 检查节点到 API Server 的连通性和证书状态。
3. 检查宿主机时间、文件系统和容器运行时日志。

## 常见原因

- kubelet 或容器运行时异常。
- 节点资源耗尽或根文件系统只读。
- 节点与控制面网络中断。
- 节点证书或时间同步异常。

## 安全缓解

- 先确认剩余容量，再通过受控流程隔离节点和迁移工作负载；不要直接重启承载唯一副本的节点。

## 恢复验证

- Ready 条件恢复并稳定，节点心跳、Pod 状态和集群容量正常。

## 回滚与升级

- 节点维护导致容量不足时停止后续维护；控制面异常立即升级平台负责人。

## 适用限制

- Ready 恢复不代表节点上的所有 Pod 已恢复，需要继续检查工作负载。
