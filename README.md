# GoOnCall Agent

> 基于 Go + Eino 的 AIOps Agent Platform —— 把告警接入、故障诊断、知识检索、工具调用、人工审批、自动处置与验证串联为可观测、可恢复、可审计的闭环。

项目已完成设计文档第 28 节全部七个阶段（Phase 1–7），覆盖 v1.0 验收标准。

## 能力总览

- **Incident**：创建 / 指纹去重 / 状态机（OPEN → INVESTIGATING → WAITING_APPROVAL → MITIGATING → VERIFYING → RESOLVED）
- **Eino Agent**：ReAct 诊断 Agent（Supervisor/Diagnosis）+ 工具调用
- **Tool Runtime**：Prometheus / RabbitMQ / Runbook / Incident 历史 / Restart Worker，带风险等级与审批门控
- **RAG**：Markdown 加载 → 分块 → Embedding → Qdrant（或内存）→ 混合检索（词法 + 向量，RRF 融合）
- **Agent Runtime**：AgentRun / Step / ToolCall 持久化 + SSE 流式事件
- **HITL**：风险策略 → 人工审批 → 批准后执行
- **自动处置**：restart_worker → 执行后验证 → 自动关闭 Incident → 生成 Postmortem
- **告警接入**：Prometheus Alertmanager webhook → Incident 创建/关闭

## 快速开始

### 1. 启动基础设施

```bash
docker compose up -d postgres redis rabbitmq qdrant prometheus grafana
```

### 2. 运行 API

```bash
export POSTGRES_DSN='postgres://gooncall:gooncall@localhost:5432/gooncall?sslmode=disable'
export LLM_BASE_URL='https://api.openai.com/v1'   # 可选
export LLM_API_KEY='sk-...'                        # 可选
export LLM_MODEL='gpt-4o'                          # 可选
go run ./cmd/api
```

> 未设置 POSTGRES_DSN 时回退内存仓库；未设置 LLM 时 analyze 仅做状态流转。

### 3. 端到端 Demo

```bash
./scripts/demo.sh
```

## API 一览

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/alerts` | Alertmanager webhook，创建/关闭 Incident |
| POST | `/api/v1/incidents` | 创建 Incident（指纹去重） |
| GET | `/api/v1/incidents` | 列表 |
| GET | `/api/v1/incidents/:id` | 详情 |
| POST | `/api/v1/incidents/:id/analyze` | 进入调查 + 触发诊断 Agent |
| POST | `/api/v1/incidents/:id/resolve` | 关闭 |
| GET | `/api/v1/runs/:id` | Agent Run 详情 |
| GET | `/api/v1/runs/:id/steps` | 步骤 |
| GET | `/api/v1/runs/:id/evidences` | 证据 |
| GET | `/api/v1/runs/:id/stream` | SSE 流式事件 |
| GET | `/api/v1/approvals/:id` | 审批详情 |
| POST | `/api/v1/approvals/:id/approve` | 批准（触发处置） |
| POST | `/api/v1/approvals/:id/reject` | 拒绝 |
| GET | `/healthz` `/readyz` | 健康检查 |

## 目录结构

```text
cmd/             # api / worker 入口
internal/
  api/           # handler / router / middleware / dto
  agent/         # runtime（Eino ReAct）/ diagnosis / supervisor
  incident/      # model / service / repository / state_machine
  tool/          # registry / prometheus / rabbitmq / runbook / incident / deployment
  knowledge/     # loader / splitter / embedding / retriever / vectorstore
  execution/     # policy / approval / executor / verifier / postmortem
  messaging/     # RabbitMQ（Phase 4+）
  storage/       # postgres / redis / qdrant
  config/        # 配置加载
migrations/      # SQL 迁移
prompts/         # Agent 提示词
docs/            # Runbook / Postmortem 知识源
deploy/          # docker / k8s / prometheus
scripts/         # demo.sh
```

## 技术栈

Go、Gin、Eino、PostgreSQL、Redis、RabbitMQ、Qdrant、Prometheus、Grafana、Docker。

## 设计文档

完整设计见《GoOnCall-Agent-v1.0-技术设计文档.md》。
