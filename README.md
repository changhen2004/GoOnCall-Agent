# GoOnCall Agent

> 基于 Go + Eino 的 AIOps Agent：告警接入 → ReAct 故障诊断（+ RAG 知识检索）→ 人工审批 → 自动处置（执行/验证/复盘）的可观测闭环。

## 已实现能力

- **Incident 生命周期**：Alertmanager webhook 创建/关闭工单，指纹去重，严格状态机 `OPEN → INVESTIGATING → WAITING_APPROVAL → MITIGATING → VERIFYING → RESOLVED/FAILED`；并发状态迁移由 version 乐观锁（CAS）保护，冲突返回 409
- **Eino ReAct 诊断 Agent**：`Incident → Agent → Tool → Result`，注册表式工具调用；未配置 LLM 时自动降级（analyze 仅做状态流转）
- **工具集**：Prometheus 指标查询 / RabbitMQ 队列检查 / Runbook 检索 / Incident 历史 / `worker.restart`（模拟重启，MEDIUM 风险，执行前强制人工审批）
- **RAG 混合检索**：Markdown 加载 → 分块 → Embedding → 内存或 Qdrant 向量库 → 词法 + 向量混合检索（RRF 融合）；向量检索候选集可配置，避免全量扫描
- **Agent Run 记录**：AgentRun / Step / ToolCall 持久化 + SSE 事件流；`max_steps` / `max_tool_calls` / 整轮诊断超时 + 单次工具超时
- **HITL 人工审批**：风险策略 → 审批（批准/拒绝）→ 批准后执行处置
- **自动处置链路**：审批通过 → 执行 `restart_worker` → 指标验证（Mock / Prometheus 可切换）→ 验证通过自动关闭 Incident 并生成 Postmortem，失败置 FAILED
- **RabbitMQ 事件驱动**：API 异步发布 `agent.requested`，Worker 消费事件并运行诊断 Agent；消费失败自动重试（最多 3 次、间隔 5s），超过后进入死信队列（DLQ），不会无限 requeue
- **可观测性**：Prometheus 指标 + `/healthz` `/readyz`
- **测试与部署**：单元测试 + E2E 流程测试；Docker Compose 一键启动（中间件版本已固定）

## 实现边界（如实说明）

- `worker.restart` 为**模拟执行**，不真实操作部署
- Supervisor 仅保留角色占位，未接入多 Agent 编排
- SSE 覆盖 Run / Step / Tool 事件流；LLM 输出为一次性返回（无 token 级流式）
- 去重基于 PostgreSQL 指纹；Redis 仅作为基础设施提供，未接入主流程
- `deploy/k8s` 为空占位，目前仅提供 Docker Compose 部署

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
| GET | `/api/v1/runs/:id/stream` | SSE 事件流 |
| GET | `/api/v1/approvals/:id` | 审批详情 |
| POST | `/api/v1/approvals/:id/approve` | 批准（触发处置） |
| POST | `/api/v1/approvals/:id/reject` | 拒绝 |
| GET | `/healthz` `/readyz` | 健康检查 |

## 目录结构

```text
cmd/             # api / worker 入口
internal/
  api/           # handler / router / middleware / dto
  agent/         # runtime（Eino ReAct）/ supervisor（占位）
  bootstrap/     # 应用装配（config/database/knowledge/tools/messaging/agent/server）
  incident/      # model / service / repository / state_machine
  tool/          # registry / prometheus / rabbitmq / runbook / incident / deployment
  knowledge/     # loader / splitter / embedding / retriever / vectorstore（含 qdrant）
  execution/     # policy / approval / executor（处置闭环）/ verifier / postmortem
  messaging/     # RabbitMQ 事件生产/消费（已接入）
  observability/ # metrics
  storage/       # postgres / redis
  config/        # 配置加载
migrations/      # SQL 迁移
prompts/         # Agent 提示词
docs/            # Runbook / Postmortem 知识源
deploy/          # docker / prometheus
scripts/         # demo.sh
tests/           # e2e 流程测试
```

## 技术栈

Go、Gin、Eino、PostgreSQL、Redis、RabbitMQ、Qdrant、Prometheus、Grafana、Docker。

## 设计文档

完整设计见《GoOnCall-Agent-v1.0-技术设计文档.md》（其中开发阶段与验收标准为规划内容，实际能力以本 README「已实现能力」与「实现边界」为准）。
