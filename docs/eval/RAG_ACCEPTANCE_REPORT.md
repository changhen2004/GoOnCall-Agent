# RAG 两层验收报告（2026-10-17）

## 验收结论

通过两层验收，保留 Qwen reranker 启用状态。

- 第一层：文档级 Recall@5 不回退，MRR 与 NDCG@5 均提升，`misses_at_5` 保持为空。
- 第二层：RabbitMQ、Kubernetes、PostgreSQL 三类真实模型诊断均将目标 Runbook 排在首位，结论引用检索证据并保留证据不足边界，未触发不符合条件的处置。

## 环境与口径

- 评测集：60 条已标注查询，RabbitMQ、Kubernetes、PostgreSQL 各 20 条。
- 知识库：15 个 Runbook 文档块。
- 基线：BM25 + 768 维向量检索 + RRF，`candidate_k=20`。
- 优化组：在同一基线上增加 `qwen3.7-text-rerank` 候选重排。
- 向量模型：`nomic-embed-text:latest`。
- 向量库：Qdrant 1.19.0；Go 客户端 1.17.1 给出版本兼容警告，但本次读写与检索成功。
- Agent 模型：`deepseek-flash`；第二层使用内存仓库，不连接生产数据库、消息队列或生产监控。

## 第一层：文档级检索

| 指标 | BM25 + 向量 + RRF | 增加 Qwen reranker | 变化 |
|---|---:|---:|---:|
| Recall@3 | 98.33% | 100.00% | +1.67 个百分点 |
| Recall@5 | 100.00% | 100.00% | 持平，通过硬门槛 |
| Recall@8 | 100.00% | 100.00% | 持平 |
| MRR | 0.9256 | 0.9917 | +7.14% |
| NDCG@5 | 0.9445 | 0.9938 | +5.22% |
| Recall@5 未命中 | 0 | 0 | 持平 |
| 60 条整批耗时 | 4.41s | 10.36s | +5.95s |

整批耗时包含 Go 启动、配置加载、Qdrant 初始化和 60 次检索，只用于本次同机对照，不等同于线上单请求延迟。

## 第二层：真实模型 Agent 诊断

| 场景 | 目标 Runbook | 首位检索结果 | 端到端耗时 | 工具调用 | 验收结果 |
|---|---|---|---:|---:|---|
| RabbitMQ 队列积压、消费者下降 | `rabbitmq-backlog.md` / `rabbitmq-consumer-count-dropped.md` | `rabbitmq-backlog.md`，最高分 0.972 | 38.40s | 8 次，0 失败 | 正确引用两份 Runbook；将消费者掉线标为待证假设；未重启 |
| Kubernetes CrashLoopBackOff | `kubernetes-pod-crashloopbackoff.md` | `kubernetes-pod-crashloopbackoff.md`，最高分 0.945 | 17.58s | 8 次，0 失败 | 区分 Exit Code 1、探针失败与 OOM；要求补日志；未重启 |
| PostgreSQL 锁争用 | `postgresql-lock-contention.md` | `postgresql-lock-contention.md`，最高分 0.978 | 16.07s | 7 次，0 失败 | 定位长事务持锁方向并要求补等待链；未重启 |

- 三个场景端到端耗时中位数：17.58s；平均值：24.02s。
- 共执行 23 次只读工具调用，失败 0 次，`worker_restart` 调用 0 次。
- 三个结论均引用正确 Runbook，并明确指出现场日志、指标或会话级证据缺口，没有把 Runbook 中的通用原因直接写成已确认根因。

## 验收中发现并修复的问题

未配置 RabbitMQ 时，API 仍注入空发布器，导致 `/analyze` 只更新 Incident 状态而跳过同步 Agent 兜底。修复为仅在真实 Producer 存在时注入 Publisher，并增加同步诊断回归测试。

## 结论边界

- 60 条查询来自 15 个 Runbook 的小型受控知识库，指标不能外推为生产流量或开放域效果。
- 第二层每类只执行 1 次真实模型诊断，能证明当前链路和证据约束通过验收，不代表长期稳定性或确定性延迟。
- 未接入真实 Kubernetes、PostgreSQL、RabbitMQ 现场指标，因此 Agent 对具体根因保持中低置信度是正确行为。

## 简历候选表述

围绕 RabbitMQ、Kubernetes、PostgreSQL 构建 60 条已标注查询的 RAG 评测集，落地 BM25 + 向量 RRF + Qwen Rerank；在 Recall@5 保持 100% 的前提下，将 MRR 从 0.926 提升至 0.992（+7.1%）、NDCG@5 从 0.945 提升至 0.994（+5.2%），并通过 3 类真实模型 Agent 诊断验收，确保正确引用 Runbook 且证据不足时不触发处置。
