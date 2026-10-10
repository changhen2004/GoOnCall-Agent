# GoOnCall Agent

> 基于 Go + Eino 的 AIOps Agent：告警接入 → ReAct 故障诊断（+ RAG 知识检索）→ 人工审批 → 自动处置（执行 / 验证 / 复盘）的可观测闭环。

## 简介

GoOnCall Agent 把运维告警处理做成一条可审计的自动链路。Alertmanager 的 webhook 创建 Incident 后，系统通过 RabbitMQ 触发诊断 Agent；Agent 以 ReAct 方式调用工具（Prometheus、RabbitMQ、Runbook、历史 Incident）收集证据并定位根因，产出处置建议。涉及变更的操作先进入人工审批，批准后才执行，再通过指标**连续验证**结果，通过则自动关闭 Incident 并生成 Postmortem。全过程落库并通过持久化查询 API 可查（SSE 仅是单进程实时视图）。

> **能力边界先说清楚**：本地 Demo 必须显式设置 `EXECUTION_MODE=mock`，此时处置为模拟执行且可不启用认证。要得到“真实处置、可查、可审计、不可重复批准”的可靠闭环，必须按「生产闭环配置」配置持久化、消息、LLM、Token、`execution.mode=http` 与 `verification.mode=prometheus`。

## 功能

- **Incident 生命周期**：告警 webhook 创建 / 关闭工单；创建后自动发布 `agent.requested`（告警即诊断）；同一指纹只允许一条非终态 Incident，终态后复发创建**新的** Incident（新周期、新 ID）；状态迁移由 `version` 乐观锁与严格状态机保护，冲突返回 409。
- **ReAct 诊断 Agent**：`Incident → Agent → Tool → Result`，注册表式工具调用；未配置 LLM 时自动降级（`analyze` 仅做状态流转）。
- **工具集**（按配置启用）：`prometheus_query`、`prometheus_alerts`、`prometheus_range_query`、`rabbitmq_inspect`、`runbook_search`、`incident_history`、`worker_restart`。
- **RAG 混合检索**：Markdown 加载 → 分块 → Embedding → 向量库（内存或 Qdrant）→ 词法 + 向量混合检索（RRF 融合）；向量候选集大小可配置，避免全量扫描；启动时按 chunk 内容哈希增量索引，未变化的 chunk 跳过重新 embedding。
- **Run 记录与实时事件**：AgentRun / Step / ToolCall 持久化，任一关键审计写入失败都会让本轮诊断失败而非静默成功；同一 Incident 已有活动 Run 时，重复消息直接成功确认，不按时间阈值抢占执行。SSE 推送 Run / Step / Tool 事件；支持 `max_steps`、`max_tool_calls`、整轮诊断超时与单次工具超时。
- **HITL 人工审批**：风险策略判定 → 审批（批准 / 拒绝）→ 批准后执行。执行权通过数据库条件更新（CAS）获得：`PENDING → APPROVED → EXECUTING → EXECUTED/FAILED/UNKNOWN`，并发或重复批准返回 409 且不会二次执行；`EXPIRED` 表示 Incident 已外部恢复或不再允许执行，`UNKNOWN` 表示外部动作结果未知，两者都需要人工核对，不自动重试。
- **处置与连续验证**：审批通过 → 执行 `worker_restart`（`execution.mode=mock` 为模拟执行；`mode=http` 调用固定 URL 的 HTTP 适配器，带 Bearer Token 与 `Idempotency-Key: <approval_id>`，target 受白名单限制）→ **连续**验证 `required_successes` 次全部通过才 `RESOLVED` 并生成 Postmortem；采集错误、空结果、`NaN`/`Inf`、验证失败一律进 `FAILED`，绝不按 0 处理。
- **RabbitMQ 事件驱动与幂等**：API 以 publisher confirm 发布 `agent.requested`（发布失败返回非 2xx，Alertmanager 会重试）；Worker 消费时先查有效 Run，重复消息直接成功确认。同一 Incident 至多一条 `RUNNING/COMPLETED` Run（数据库部分唯一索引）。消费失败重试（最多 3 次、间隔 5s），重投同样走 publisher confirm，**确认进入 DLQ 之后**才把 Incident 置为 `FAILED`。
- **查询 API**：不依赖 SSE 的持久化查询（Incident 的 Run / 状态与处置事件、`POSTMORTEM_GENERATED` 复盘事件、Run 的 ToolCall / 审批、待审批列表），进程重启后仍可读到完整链路。
- **最小认证**：webhook（`X-GoOnCall-Webhook-Token`）、读（`Authorization: Bearer`）、admin（Incident 写操作与审批）三类 Token，均使用常量时间比较；`approved_by` 来自服务端配置，请求体伪造无效。
- **可观测性**：Prometheus 指标（各状态 Incident / 审批数、DLQ 与重试次数、处置与验证结果、闭环总耗时）+ `/healthz` `/readyz`（就绪检查覆盖 PostgreSQL、RabbitMQ 与危险配置）+ Grafana 概览面板。

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
make up                      # 启动 postgres / rabbitmq / prometheus / alertmanager / grafana
                             # Redis / Qdrant 不在默认范围内：需要时用
                             # `EXECUTION_MODE=mock docker compose --profile rag up -d`

export POSTGRES_DSN='postgres://gooncall:gooncall@localhost:5432/gooncall?sslmode=disable'
export RABBITMQ_URL='amqp://gooncall:gooncall@localhost:5672/'
export EXECUTION_MODE=mock
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
EXECUTION_MODE=mock docker compose up -d --build
```

PostgreSQL 容器只在首次创建数据卷时按顺序执行 `migrations/*.sql`。升级已有数据库时，请在启动 API / Worker 前由发布流程只执行尚未应用的 SQL 迁移；服务进程不会自动改 schema。

### 端到端 Demo

```bash
EXECUTION_MODE=mock SERVER_PORT=8082 go run ./cmd/api   # 终端 A
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
| `POSTGRES_DSN` | PostgreSQL DSN，未设置时回退内存仓库（仅 Demo） |
| `RABBITMQ_URL` / `RABBITMQ_MANAGEMENT_URL` / `RABBITMQ_USERNAME` / `RABBITMQ_PASSWORD` | RabbitMQ 连接与队列检查 |
| `PROMETHEUS_URL` | Agent 工具与验证采集使用的 Prometheus |
| `LLM_BASE_URL` / `LLM_API_KEY` / `LLM_MODEL` / `LLM_EMBEDDING_MODEL` | LLM 配置，为空则不装配诊断 Agent |
| `ALERT_WEBHOOK_TOKEN` / `API_READ_TOKEN` / `API_ADMIN_TOKEN` / `API_ADMIN_ACTOR` | 三类最小认证 Token 与审批人标识 |
| `EXECUTION_MODE` | 处置通道：`mock`（显式 Demo，模拟重启）/ `http`（固定 URL 真实处置）；必须设置 |
| `RESTART_WORKER_URL` / `RESTART_WORKER_TOKEN` | `execution.mode=http` 时的固定处置 endpoint 与 Bearer Token |
| `RESTART_WORKER_ALLOWED_TARGETS` | 允许重启的 target 白名单（逗号分隔，`http` 模式必填） |
| `VERIFICATION_MODE` | 验证采集器：`mock`（默认）/ `prometheus` |
| `CONFIG_PATH` | 配置文件路径，默认 `configs/config.yaml` |
| `REDIS_ADDR` / `REDIS_PASSWORD` / `QDRANT_URL` | 可选 RAG 组件；默认不启动、也不参与闭环 |

`configs/config.yaml` 另含：`agent`（`max_steps`、`max_tool_calls`、整轮 `timeout_seconds`）、`tool.timeout_seconds`、`execution.timeout_seconds`，以及 `verification.attempts` / `interval_seconds` / `required_successes`（连续验证次数与间隔）。

### 生产闭环配置

`execution.mode=http` 时配置校验会强制要求（缺任意一项**启动失败**，而不是运行到一半才发现）：

1. `verification.mode=prometheus`（Mock 验证绝不允许关闭真实 Incident）且 `PROMETHEUS_URL` 非空；
2. `RESTART_WORKER_URL` 与 `RESTART_WORKER_ALLOWED_TARGETS` 非空（白名单为空则拒绝所有 target）；
3. `ALERT_WEBHOOK_TOKEN` / `API_READ_TOKEN` / `API_ADMIN_TOKEN` 全部非空；
4. PostgreSQL DSN、RabbitMQ URL、LLM BaseURL/Model 全部非空；
5. `verification.required_successes ≤ verification.attempts`（不做静默降级，否则等于降低安全强度）。

```bash
export ALERT_WEBHOOK_TOKEN=...  API_READ_TOKEN=...  API_ADMIN_TOKEN=...  API_ADMIN_ACTOR=ops@example.com
export EXECUTION_MODE=http  RESTART_WORKER_URL=https://deploy.internal/api/restart  RESTART_WORKER_TOKEN=...
export RESTART_WORKER_ALLOWED_TARGETS=resource-community-worker
export VERIFICATION_MODE=prometheus  PROMETHEUS_URL=http://prometheus:9090
export POSTGRES_DSN=...  RABBITMQ_URL=...  LLM_BASE_URL=...  LLM_MODEL=...
```

Token / 凭证不会写入日志、错误或响应；`/readyz` 只返回 `ok` / `unavailable`，详细原因写服务端日志。

## API

认证列：`webhook` = `X-GoOnCall-Webhook-Token`；`read` = `Authorization: Bearer <API_READ_TOKEN>`（admin Token 同时具备读权限）；`admin` = 只接受 admin Token。

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| `POST` | `/api/v1/alerts` | webhook | Alertmanager webhook，创建 / 关闭 Incident（发布失败返回非 2xx） |
| `POST` | `/api/v1/incidents` | admin | 创建 Incident（指纹去重） |
| `GET` | `/api/v1/incidents` | read | 列表 |
| `GET` | `/api/v1/incidents/:id` | read | 详情 |
| `POST` | `/api/v1/incidents/:id/analyze` | admin | 进入调查并触发诊断 Agent |
| `POST` | `/api/v1/incidents/:id/resolve` | admin | 关闭 |
| `GET` | `/api/v1/incidents/:id/runs` | read | Run 列表（StartedAt 倒序） |
| `GET` | `/api/v1/incidents/:id/events` | read | Incident 审计事件（时间正序） |
| `GET` | `/api/v1/runs/:id` | read | Agent Run 详情 |
| `GET` | `/api/v1/runs/:id/steps` | read | 步骤 |
| `GET` | `/api/v1/runs/:id/evidences` | read | 证据 |
| `GET` | `/api/v1/runs/:id/tool-calls` | read | 工具调用（CreatedAt 正序） |
| `GET` | `/api/v1/runs/:id/approvals` | read | 该 Run 的审批（CreatedAt 正序） |
| `GET` | `/api/v1/runs/:id/stream` | read | SSE 事件流（**单进程实验能力**） |
| `GET` | `/api/v1/approvals` | read | 待审批列表（`?status=PENDING`） |
| `GET` | `/api/v1/approvals/:id` | read | 审批详情 |
| `POST` | `/api/v1/approvals/:id/approve` | admin | 批准（触发处置）；`approved_by` 取自服务端配置 |
| `POST` | `/api/v1/approvals/:id/reject` | admin | 拒绝 |
| `GET` | `/healthz` `/readyz` | - | 存活 / 就绪（依赖或配置异常时 `/readyz` 返回 503） |

父资源不存在返回 404（不会伪装成空数组），空集合返回 `[]`。

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
EXECUTION_MODE=mock docker compose up -d alertmanager
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
├── tests/               # integration（HTTP 处置 + Prometheus stub 全链路）
├── docker-compose.yml
└── Makefile
```

## 测试与 CI

```bash
make test                       # go test ./... -count=1
make test-integration           # 集成测试（本地 httptest stub，无需 docker 或外部凭证）
make vet && make fmt            # go vet / gofmt
```

CI（`.github/workflows/ci.yml`）执行 `go mod tidy` 检查、`gofmt` 格式检查、`go vet`、`go build ./...` 与单元测试。

## 实现边界

以下为如实说明，避免与设计文档混淆：

- **Demo 不是生产**：显式设置 `execution.mode=mock` 时 `worker_restart` 是模拟执行、验证用固定指标，且允许不启用认证；未配置 DSN 时使用内存仓库（进程重启即丢失）。Compose API 默认只发布到 `127.0.0.1`，仍不要通过反向代理暴露无认证 Demo。
- **可靠闭环要求显式配置**：真实处置需要 `execution.mode=http` + `verification.mode=prometheus` + 三个 Token + target 白名单（见「生产闭环配置」）。此时处置走固定 URL（不接受任意 URL / Shell / Kubernetes SDK），幂等键为 `approval_id`，只有连续验证通过才会 `RESOLVED`。
- **SSE 是单进程实验能力**：事件只在本进程内存广播，多副本部署时不会跨进程同步；生产闭环请使用持久化查询 API（`/incidents/:id/events`、`/runs/:id/tool-calls`、`/approvals?status=PENDING`）。
- **只支持一种自动处置**：`restart_worker`。LLM 只能请求创建审批，不能提供 URL、命令或凭证。
- **不包含**：前端、通知平台、Outbox、多 Agent 编排（Supervisor 仅为角色占位）、token 级流式输出、向量 RAG 调优。
- 去重与审计基于 PostgreSQL（部分唯一索引 + 审计事件）；schema 只由 `migrations/*.sql` 管理，应在 API / Worker 启动前完成。Redis 仅作为可选 RAG 组件，不参与闭环。
- `deploy/k8s` 为空占位，目前仅提供 Docker Compose 部署。

## 设计文档

完整设计见《GoOnCall-Agent-v1.0-技术设计文档.md》。其中开发阶段与验收标准为规划内容，实际能力以本 README 的「功能」与「实现边界」为准。
