# GoOnCall Agent

> 基于 Go + Eino 的 AIOps Agent：告警接入 → ReAct 故障诊断（+ RAG 知识检索）→ 人工审批 → 自动处置（执行 / 验证 / 复盘）的可观测闭环。

## 简介

GoOnCall Agent 把运维告警处理做成一条可审计的自动链路。Alertmanager 的 webhook 创建 Incident 后，系统通过 RabbitMQ 触发诊断 Agent；Agent 以 ReAct 方式调用工具（Prometheus、RabbitMQ、Runbook、历史 Incident）收集证据并定位根因，产出处置建议。涉及变更的操作先进入人工审批，批准后才执行，再通过指标验证结果，通过则自动关闭 Incident 并生成 Postmortem。全过程落库并支持 SSE 实时查看。

## 功能

- **Incident 生命周期**：告警 webhook 创建 / 关闭工单；创建与重开自动发布 `agent.requested`（告警即诊断）；按指纹去重，告警复发（同指纹已 RESOLVED）自动重开为 OPEN；状态迁移由 `version` 乐观锁保护，冲突返回 409。
- **ReAct 诊断 Agent**：`Incident → Agent → Tool → Result`，注册表式工具调用；未配置 LLM 时自动降级（`analyze` 仅做状态流转）。
- **工具集**（按配置启用）：`prometheus_query`、`prometheus_alerts`、`prometheus_range_query`、`rabbitmq_inspect`、`runbook_search`、`incident_history`、`worker_restart`。
- **RAG 混合检索**：Markdown 加载 → 分块 → Embedding → 向量库（内存或 Qdrant）→ 词法 + 向量混合检索（RRF 融合）；向量候选集大小可配置，避免全量扫描；启动时按 chunk 内容哈希增量索引，未变化的 chunk 跳过重新 embedding。
- **Run 记录与实时事件**：AgentRun / Step / ToolCall 持久化，SSE 推送 Run / Step / Tool 事件；支持 `max_steps`、`max_tool_calls`、整轮诊断超时与单次工具超时。
- **HITL 人工审批**：风险策略判定 → 审批（批准 / 拒绝）→ 批准后执行；执行状态落库（`APPROVED → EXECUTING → EXECUTED/FAILED`），审计链完整。
- **自动处置闭环**：审批通过 → 执行 `worker_restart` → 指标验证（Mock / Prometheus 可切换，验证 PromQL 可配置）→ 通过则自动关闭 Incident 并生成 Postmortem，失败置 FAILED。
- **RabbitMQ 事件驱动**：API 异步发布 `agent.requested`，Worker 消费并运行诊断 Agent；消费失败重试（最多 3 次、间隔 5s），超过后进入死信队列（DLQ），不会无限 requeue。
- **可观测性**：Prometheus 指标 + `/healthz` `/readyz` + Grafana 概览面板。

## 架构

```mermaid
flowchart TD
    Biz[业务系统 /metrics] --> Prom[Prometheus]
    Prom --> AM[Alertmanager]
    AM -->|webhook| API[GoOnCall API]
    API --> DB[(PostgreSQL)]
    API -->|agent.requested| MQ[[RabbitMQ]]
    MQ --> Worker[Worker]
    Worker --> Agent[ReAct 诊断 Agent]
    Agent -->|工具调用| Tools[Prometheus / RabbitMQ / Runbook / Incident]
    Agent -->|检索| RAG[(向量库：内存 / Qdrant)]
    Agent -->|变更操作| Approval{人工审批}
    Approval -->|批准| Exec[执行处置 worker_restart]
    Exec --> Verify[指标验证]
    Verify -->|通过| Close[关闭 Incident + Postmortem]
    Verify -->|失败| Fail[FAILED]
    Worker --> DB
    API --> Grafana[Grafana 概览面板]
```

Incident 严格状态机（仅允许主流程，禁止 `INVESTIGATING` 直接 `RESOLVED`）：

```mermaid
stateDiagram-v2
    [*] --> OPEN
    OPEN --> INVESTIGATING
    INVESTIGATING --> WAITING_APPROVAL
    WAITING_APPROVAL --> MITIGATING
    WAITING_APPROVAL --> FAILED
    MITIGATING --> VERIFYING
    MITIGATING --> FAILED
    VERIFYING --> RESOLVED
    VERIFYING --> FAILED
    RESOLVED --> [*]
    FAILED --> [*]
```

## 快速开始

### 方式一：本地运行（推荐）

```bash
make up                      # 启动 postgres / redis / rabbitmq / qdrant / prometheus / alertmanager / grafana

export POSTGRES_DSN='postgres://gooncall:gooncall@localhost:5432/gooncall?sslmode=disable'
# 可选：配置 LLM 后才会装配诊断 Agent；不配置时 analyze 仅做状态流转
export LLM_BASE_URL='https://api.openai.com/v1'
export LLM_API_KEY='sk-...'
export LLM_MODEL='gpt-4o'

go run ./cmd/api             # API，默认 :8080
go run ./cmd/worker          # Worker（消费事件并运行 Agent）
```

> 本机 `8080` 常被 GoCommunity backend 占用，可用 `SERVER_PORT=8082 go run ./cmd/api`；
> 此时 `scripts/demo.sh` 默认访问 `8082`。

### 方式二：Docker Compose 全量启动

```bash
docker compose up -d --build
```

### 端到端 Demo

```bash
SERVER_PORT=8082 go run ./cmd/api   # 终端 A
./scripts/demo.sh                   # 终端 B：模拟告警 → 查看 Incident → 分析 → 告警恢复
```

| 服务 | 地址 | 默认凭据 |
|---|---|---|
| 后端 API | http://localhost:8080 | - |
| 健康检查 | http://localhost:8080/healthz · `/readyz` | - |
| Prometheus | http://localhost:9090 | - |
| Alertmanager | http://localhost:9093 | - |
| Grafana | http://localhost:3000 | `admin` / `admin` |
| RabbitMQ 管理台 | http://localhost:15672 | `gooncall` / `gooncall` |
| Qdrant | http://localhost:6333 | - |

> 与 GoCommunity 共存时，为 Grafana 指定 `GRAFANA_PORT=3005` 启动。

## 配置

主配置为 `configs/config.yaml`，使用 `${VAR}` 引用环境变量。

| 变量 | 说明 |
|---|---|
| `SERVER_PORT` | API 监听端口，默认 8080 |
| `POSTGRES_DSN` | PostgreSQL DSN，未设置时回退内存仓库 |
| `REDIS_ADDR` / `REDIS_PASSWORD` | Redis 连接 |
| `RABBITMQ_URL` / `RABBITMQ_MANAGEMENT_URL` / `RABBITMQ_USERNAME` / `RABBITMQ_PASSWORD` | RabbitMQ 连接与队列检查 |
| `PROMETHEUS_URL` | Agent 工具与验证采集使用的 Prometheus |
| `QDRANT_URL` | Qdrant 地址 |
| `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` / `LLM_EMBEDDING_MODEL` | LLM 配置，为空则不装配诊断 Agent |
| `VERIFICATION_MODE` | 验证采集器：`mock`（默认）/ `prometheus` |
| `CONFIG_PATH` | 配置文件路径，默认 `configs/config.yaml` |

Agent 运行时约束见 `configs/config.yaml` 的 `agent`（`max_steps`、`max_tool_calls`、整轮 `timeout_seconds`）与 `tool.timeout_seconds`。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/v1/alerts` | Alertmanager webhook，创建 / 关闭 Incident |
| `POST` | `/api/v1/incidents` | 创建 Incident（指纹去重） |
| `GET` | `/api/v1/incidents` | 列表 |
| `GET` | `/api/v1/incidents/:id` | 详情 |
| `POST` | `/api/v1/incidents/:id/analyze` | 进入调查并触发诊断 Agent |
| `POST` | `/api/v1/incidents/:id/resolve` | 关闭 |
| `GET` | `/api/v1/runs/:id` | Agent Run 详情 |
| `GET` | `/api/v1/runs/:id/steps` | 步骤 |
| `GET` | `/api/v1/runs/:id/evidences` | 证据 |
| `GET` | `/api/v1/runs/:id/stream` | SSE 事件流 |
| `GET` | `/api/v1/approvals/:id` | 审批详情 |
| `POST` | `/api/v1/approvals/:id/approve` | 批准（触发处置） |
| `POST` | `/api/v1/approvals/:id/reject` | 拒绝 |
| `GET` | `/healthz` `/readyz` | 健康检查 |

## 告警链路（业务系统 → Prometheus → Alertmanager → GoOnCall）

```text
业务系统 /metrics → Prometheus（规则评估）→ Alertmanager（分组去重）→ webhook → GoOnCall /api/v1/alerts → Incident
```

| 环节 | 当前落地 |
|---|---|
| 业务系统 | GoCommunity backend（`resource_community_go`，`/metrics` 暴露 `resource_community_http_*`） |
| Prometheus | GoCommunity 自带实例（宿主机 `:9091`） |
| Alertmanager | 本仓库 compose 服务（宿主机 `:9093`），配置 `deploy/alertmanager/alertmanager.yml` |
| 对接 | GoCommunity Prometheus `alerting → host.docker.internal:9093`；Alertmanager webhook → `http://gooncall-api:8080/api/v1/alerts`（`send_resolved: true`，恢复时关闭 Incident） |

```bash
docker compose up -d alertmanager
curl localhost:9093/-/healthy          # Alertmanager 存活
curl localhost:9093/api/v2/alerts      # Alertmanager 实际收到的告警
```

> Alertmanager 不支持配置内 `${VAR}` 插值，webhook URL 硬编码在 `deploy/alertmanager/alertmanager.yml`。API 在宿主机运行时改为 `http://host.docker.internal:8082/api/v1/alerts`（compose 已配置 `extra_hosts`）。

## 项目结构

```text
GoOnCall-Agent/
├── cmd/
│   ├── api/             # HTTP API 入口
│   └── worker/          # 消费事件并运行 Agent 的入口
├── internal/
│   ├── api/             # handler / router / middleware / dto
│   ├── agent/
│   │   ├── diagnosis/   # Eino ReAct 诊断 Agent
│   │   ├── runtime/     # 运行时：Run 录制、SSE 广播、工具限流与超时
│   │   └── supervisor/  # 多 Agent 编排占位
│   ├── bootstrap/       # 应用装配（config / database / knowledge / tools / messaging / agent / server）
│   ├── config/          # 配置加载
│   ├── execution/       # policy / approval / executor（处置编排）/ verifier / postmortem
│   ├── incident/        # model / repository / service / state_machine
│   ├── knowledge/       # loader / splitter / embedding / retriever / vectorstore（含 qdrant）
│   ├── messaging/       # 事件生产 / 消费、重试与 DLQ
│   ├── observability/   # Prometheus 指标
│   ├── storage/         # postgres / redis
│   └── tool/            # registry / prometheus / rabbitmq / runbook / incident / deployment
├── configs/config.yaml  # 主配置
├── deploy/              # docker / prometheus / alertmanager / grafana / k8s（占位）
├── migrations/          # SQL 迁移
├── prompts/             # Agent 提示词
├── docs/                # runbooks / postmortems / architecture 知识源
├── scripts/demo.sh      # 端到端 Demo
├── tests/               # e2e / integration
├── docker-compose.yml
└── Makefile
```

## 测试与 CI

```bash
make test                       # go test ./... -count=1
make test-integration           # 集成测试（需 docker compose 环境）
make vet && make fmt            # go vet / gofmt
```

CI（`.github/workflows/ci.yml`）执行 `go mod tidy` 检查、`gofmt` 格式检查、`go vet`、`go build ./...` 与单元测试。

## 实现边界

以下为如实说明，避免与设计文档混淆：

- `worker_restart` 为**模拟执行**，不真实操作部署。
- Supervisor 仅保留角色占位，未接入多 Agent 编排。
- SSE 覆盖 Run / Step / Tool 事件流；LLM 输出为一次性返回，无 token 级流式。
- 去重基于 PostgreSQL 指纹；Redis 仅作为基础设施提供，未接入主流程。
- `deploy/k8s` 为空占位，目前仅提供 Docker Compose 部署。

## 设计文档

完整设计见《GoOnCall-Agent-v1.0-技术设计文档.md》。其中开发阶段与验收标准为规划内容，实际能力以本 README 的「功能」与「实现边界」为准。
