---
doc_id: runbook.kubernetes.pod_crashloopbackoff
doc_type: runbook
service: generic
component: kubernetes
incident_type: pod_crashloopbackoff
product_version: "Kubernetes current"
severity: P1
source_kind: official
source_url: https://kubernetes.io/docs/tasks/debug/debug-application/debug-running-pod/
last_verified_at: 2026-10-16
tags: [kubernetes, pod, crashloopbackoff, logs]
---
# Kubernetes Pod CrashLoopBackOff

## 触发条件

- Pod 状态为 `CrashLoopBackOff`，重启次数持续增加。
- 可用副本不足或服务错误率上升。

## 用户影响

- 工作负载容量下降，严重时服务完全不可用。

## 快速确认

1. 查看 Pod 状态、容器状态、上次退出原因和退出码。
2. 查看 Pod 事件以及当前和前一个容器实例的日志。
3. 检查最近部署、配置和 Secret 变更。

## 诊断步骤

1. 区分应用启动失败、探针失败、OOMKilled 和依赖不可用。
2. 检查入口命令、挂载、权限、环境变量与监听端口。
3. 对比同版本正常 Pod，确认问题是否局限于节点。

## 常见原因

- 进程启动即退出或配置缺失。
- liveness probe 参数错误。
- 内存超限或依赖服务不可达。
- 新镜像与配置不兼容。

## 安全缓解

- 通过受控发布通道回滚最近版本或修正配置；不要反复删除 Pod 掩盖持续失败。

## 恢复验证

- Pod Ready 且重启次数不再增加，可用副本、错误率和延迟恢复。

## 回滚与升级

- 回滚无效或多个版本均失败时升级应用与平台负责人共同排查。

## 适用限制

- `CrashLoopBackOff` 是退避状态，不是根因，必须读取终止原因和日志。
