---
doc_id: runbook.kubernetes.service_unreachable
doc_type: runbook
service: generic
component: kubernetes
incident_type: service_unreachable
product_version: "Kubernetes current"
severity: P1
source_kind: official
source_url: https://kubernetes.io/docs/tasks/debug/debug-application/debug-service/
last_verified_at: 2026-10-16
tags: [kubernetes, service, endpointslice, dns, network]
---
# Kubernetes Service 无法访问

## 触发条件

- 集群内通过 Service DNS 或 ClusterIP 无法访问后端。
- 连接超时、拒绝或 DNS 解析失败。

## 用户影响

- 服务调用失败，依赖链出现错误或超时。

## 快速确认

1. 确认 Service 存在，端口和 targetPort 正确。
2. 检查 EndpointSlice 是否包含 Ready 的后端地址。
3. 分别测试 Pod IP、Service IP 和 DNS 名称。

## 诊断步骤

1. 核对 Service selector 与 Pod labels。
2. 检查 Pod readiness、监听地址和容器端口。
3. 检查 NetworkPolicy、DNS 和 Service 代理实现。

## 常见原因

- selector 不匹配导致 EndpointSlice 为空。
- 后端 Pod 未 Ready 或端口配置错误。
- DNS、NetworkPolicy 或 kube-proxy/CNI 异常。
- 调用方使用了错误的 namespace 或服务名。

## 安全缓解

- 通过发布流程修正 selector、端口或策略；不要绕过网络策略长期直连 Pod IP。

## 恢复验证

- DNS、ClusterIP 和后端健康检查均通过，调用错误率恢复。

## 回滚与升级

- 最近 Service 或 NetworkPolicy 变更导致故障时回滚；集群性网络问题升级平台负责人。

## 适用限制

- Service 可访问不代表应用返回内容正确，还需检查业务健康指标。
