# GoOnCall Agent v1.0 技术设计文档

> 基于 Go + Eino 的 AIOps Agent Platform
>
> Version: v1.0.0  
> Status: Design  
> Target: Go 后端实习 / 秋招项目

---

## 1. 项目概述

### 1.1 项目定位

GoOnCall Agent 是一个面向服务运维场景的 AIOps Agent Platform，负责将：

```text
告警接入
  ↓
Incident 创建
  ↓
Agent 分析
  ↓
知识检索
  ↓
监控 / 日志 / MQ / 发布工具调用
  ↓
排障计划
  ↓
风险判断
  ↓
人工审批
  ↓
自动处置
  ↓
结果验证
  ↓
Incident 关闭
  ↓
Postmortem / 知识沉淀
```

统一为可观测、可恢复、可审计的 Agent 执行闭环。

项目不以“AI 聊天”为主要目标，而以“AI 驱动运维任务执行”为核心。

### 1.2 与现有 OnCallAgent 的关系

现有 OnCallAgent 已具备以下能力：

- Prometheus 告警读取
- Runbook 知识库
- 本地词法检索
- Qdrant + Embedding 混合检索
- Tool Calling
- Plan-Execute-Replan
- MCP 工具接入
- SSE 流式输出
- PostgreSQL 持久化
- Agent Run / Evidence
- GoCommunity 联动场景

新版本不直接照搬，而是进行架构迁移：

| 旧 OnCallAgent | GoOnCall Agent v1.0 |
|---|---|
| Python / FastAPI | Go / Gin |
| ChatAgent | Eino Agent / ADK |
| Plan Agent | Eino Workflow / Graph / Agent |
| Python Tool | Go Tool Runtime |
| KnowledgeIndex | Retriever + Qdrant |
| `/chatStream` | SSE Agent Run Stream |
| `/plan` | Incident Analysis |
| Prometheus Tool | Go Prometheus Tool |
| MCP Tool | Eino MCP Tool |
| PostgreSQL Store | Go Repository + PostgreSQL |
| 内存 Agent Run | Redis + PostgreSQL |
| 单体 Service | API + Agent Runtime + Worker |

---

## 2. v1.0 目标与非目标

### 2.1 v1.0 目标

必须完成：

1. Prometheus / 手工 API 创建 Incident。
2. Incident 标准化与去重。
3. Eino Supervisor / Diagnosis Agent。
4. Prometheus、RabbitMQ、Runbook 三类核心工具。
5. RAG 检索 Runbook / Incident / Postmortem。
6. Agent Run / Step / Evidence 持久化。
7. SSE 实时展示 Agent 执行过程。
8. RabbitMQ 异步事件驱动。
9. Tool Policy：权限、超时、幂等、审计。
10. 基于 Eino Interrupt / Resume 实现人工审批。
11. 一个可控的自动处置动作：例如 Restart Worker。
12. 执行后验证指标并自动关闭 Incident。
13. Prometheus + Grafana 监控 GoOnCall 自身。

### 2.2 v1.0 非目标

暂不实现：

- 大规模多租户
- 真正生产级多集群 Kubernetes 控制平面
- 自动修改 Git 仓库代码
- 自动修改生产配置
- 任意 Shell 执行
- 自动删除 MQ 数据
- 复杂 RBAC / SSO
- 复杂计费系统

这些属于 v2.x 或以后版本。

---

## 3. 设计原则

### 3.1 Agent 决策，系统执行

LLM 只负责：

```text
理解 → 规划 → 选择工具 → 分析结果
```

Go Runtime 负责：

```text
权限 → 校验 → 超时 → 执行 → 审计 → 状态持久化
```

禁止让 LLM 直接获得数据库、Shell、Kubernetes 等底层权限。

### 3.2 Read Tool 与 Action Tool 分离

```text
Read Only
├── prometheus.query
├── rabbitmq.inspect
├── runbook.search
├── incident.history
└── deployment.get

Action
├── worker.restart
├── deployment.scale
└── deployment.rollback
```

### 3.3 默认安全

Action Tool 默认：

```text
RequireApproval = true
```

只有 LOW 风险、明确白名单的操作可以免审批。

### 3.4 可解释

每个结论都尽量绑定 Evidence：

```text
Conclusion
  ├── Metric Evidence
  ├── Log Evidence
  ├── Runbook Evidence
  └── Tool Result Evidence
```

### 3.5 可恢复

Agent Run 必须支持：

```text
Running
 ↓
Interrupted
 ↓
Checkpoint
 ↓
Resume
```

Eino 当前提供 Agent/ADK、Tool、Workflow、上下文管理以及 Interrupt/Resume 能力，并有 HITL、多 Agent、SSE 等官方示例，因此 v1.0 使用这些原生能力，而不是自行重复实现 Agent 基础设施。

---

## 4. 总体架构

```text
                           ┌────────────────────┐
                           │ Web / CLI / API    │
                           └─────────┬──────────┘
                                     │
                                HTTP / SSE
                                     │
                           ┌─────────▼─────────┐
                           │       Gin API     │
                           └─────────┬─────────┘
                                     │
                 ┌───────────────────┼───────────────────┐
                 │                   │                   │
          ┌──────▼──────┐     ┌──────▼──────┐     ┌──────▼──────┐
          │ Incident    │     │ Agent API   │     │ Approval API│
          │ Service     │     │             │     │             │
          └──────┬──────┘     └──────┬──────┘     └──────┬──────┘
                 │                   │                   │
                 └───────────────────┼───────────────────┘
                                     │
                              ┌──────▼──────┐
                              │ Agent Runtime│
                              │    Eino      │
                              └──────┬──────┘
                                     │
             ┌───────────────────────┼───────────────────────┐
             │                       │                       │
      ┌──────▼───────┐       ┌──────▼───────┐       ┌──────▼──────┐
      │ Supervisor   │       │ Diagnosis    │       │ Action      │
      │ Agent        │       │ Agent        │       │ Agent       │
      └──────┬───────┘       └──────┬───────┘       └──────┬──────┘
             │                       │                       │
             └───────────────────────┼───────────────────────┘
                                     │
                              Tool Runtime
                                     │
       ┌───────────────┬─────────────┼─────────────┬───────────────┐
       │               │             │             │               │
 ┌─────▼─────┐   ┌─────▼─────┐ ┌────▼─────┐ ┌────▼──────┐  ┌─────▼─────┐
 │Prometheus │   │ RabbitMQ  │ │  RAG      │ │ Deployment│  │ Incident  │
 │Tool       │   │Tool       │ │ Retriever │ │ Tool      │  │ Tool      │
 └─────┬─────┘   └─────┬─────┘ └────┬─────┘ └────┬──────┘  └─────┬─────┘
       │               │             │             │               │
       └───────────────┴─────────────┼─────────────┴───────────────┘
                                     │
                            ┌────────▼────────┐
                            │ Policy / Audit  │
                            └────────┬────────┘
                                     │
                              ┌──────▼──────┐
                              │  RabbitMQ   │
                              └──────┬──────┘
                                     │
                              ┌──────▼──────┐
                              │   Worker    │
                              └──────┬──────┘
                                     │
                        ┌────────────┼─────────────┐
                        │            │             │
                     Redis     PostgreSQL       Qdrant
                        │            │             │
                        └────────────┼─────────────┘
                                     │
                              Observability
                                     │
                         Prometheus + Grafana
```

---

## 5. 核心领域模型

### 5.1 Incident

```go
type Incident struct {
    ID          string
    Service     string
    Severity    string
    Title       string
    Description string
    AlertName   string
    Fingerprint string
    Status      string
    StartedAt   time.Time
    ResolvedAt  *time.Time
    CreatedAt   time.Time
    UpdatedAt   time.Time
}
```

状态：

```text
OPEN
INVESTIGATING
WAITING_APPROVAL
MITIGATING
VERIFYING
RESOLVED
FAILED
CANCELLED
```

### 5.2 AgentRun

```go
type AgentRun struct {
    ID          string
    IncidentID  string
    AgentType   string
    Status      string
    Goal        string
    CurrentStep int
    StartedAt   time.Time
    FinishedAt  *time.Time
    Error       string
}
```

### 5.3 AgentStep

```go
type AgentStep struct {
    ID        string
    RunID     string
    StepIndex int
    Agent     string
    Action    string
    Status    string
    Input     string
    Output    string
    Duration  int64
}
```

### 5.4 Evidence

```go
type Evidence struct {
    ID         string
    RunID      string
    Type       string
    Source     string
    Content    string
    Metadata   map[string]any
    CreatedAt  time.Time
}
```

Evidence Type：

```text
METRIC
LOG
RUNBOOK
HISTORY
TOOL_RESULT
DEPLOYMENT
```

### 5.5 ToolCall

```go
type ToolCall struct {
    ID         string
    RunID      string
    ToolName   string
    RiskLevel  string
    Arguments  string
    Result     string
    Status     string
    DurationMs int64
    CreatedAt  time.Time
}
```

### 5.6 Approval

```go
type Approval struct {
    ID         string
    RunID      string
    ToolCallID string
    Action     string
    Reason     string
    Status     string
    ApprovedBy string
    CreatedAt  time.Time
    ApprovedAt *time.Time
}
```

---

## 6. Agent 架构

### 6.1 Supervisor Agent

职责：

- 理解 Incident
- 决定下一阶段
- 调度 Diagnosis / Action / Review
- 控制最大循环次数

示意：

```text
Incident
  ↓
Supervisor
  ├── Need diagnosis → Diagnosis Agent
  ├── Need knowledge → Knowledge Tool
  ├── Need action    → Action Agent
  ├── Need review    → Reviewer
  └── Complete       → Finalize
```

### 6.2 Diagnosis Agent

目标不是直接得出答案，而是持续寻找证据。

```text
Hypothesis
    ↓
Query Metrics
    ↓
Query RabbitMQ / Logs
    ↓
Search Runbook
    ↓
Compare Evidence
    ↓
Update Hypothesis
```

### 6.3 Action Agent

负责将“排障建议”转换为结构化 Action。

```json
{
  "action": "restart_worker",
  "target": "resource-community-worker",
  "reason": "consumer count dropped",
  "risk": "MEDIUM",
  "requires_approval": true
}
```

### 6.4 Reviewer Agent

负责回答：

```text
1. 是否有足够 Evidence？
2. 当前 Root Cause 是否可信？
3. Action 是否合理？
4. 风险是否可接受？
5. 执行后如何验证？
```

---

## 7. Eino Agent Runtime

### 7.1 Runtime 组件

```text
AgentRuntime
├── Runner
├── SessionManager
├── StateStore
├── ToolRegistry
├── ToolExecutor
├── PolicyEngine
├── EventPublisher
├── EvidenceStore
└── StreamBroker
```

### 7.2 Agent 执行模型

```text
Run Created
   ↓
Load Incident Context
   ↓
Planner / Supervisor
   ↓
Tool Call?
 ├── No → Finalize
 └── Yes
       ↓
   Policy Check
       ↓
   Read / Action?
   ├── Read → Execute
   └── Action
          ↓
       Approval?
       ├── No → Execute
       └── Yes
              ↓
         Interrupt
              ↓
         Checkpoint
              ↓
         Wait
              ↓
         Resume
              ↓
          Execute
       ↓
Record Evidence
       ↓
Verify
       ↓
Re-plan
```

### 7.3 ReAct 与 Workflow 的使用边界

使用 ReAct：

```text
动态诊断
动态选择查询工具
```

使用 Workflow / Graph：

```text
Incident 生命周期
审批
执行
验证
```

原则：

> 不让 LLM 决定所有流程；确定性的业务状态机交给 Go Workflow / Service。

---

## 8. Tool Runtime 设计

### 8.1 Tool 接口

建议统一封装：

```go
type Tool interface {
    Name() string
    Description() string
    RiskLevel() string
    Execute(ctx context.Context, input json.RawMessage) (ToolResult, error)
}
```

Eino 层负责把业务 Tool 暴露为 Agent Tool。

### 8.2 Tool Registry

```go
type Registry struct {
    tools map[string]Tool
}
```

初始化：

```text
Register(
    PrometheusTool,
    RabbitMQTool,
    RunbookSearchTool,
    IncidentHistoryTool,
    RestartWorkerTool,
)
```

### 8.3 Tool Middleware

统一执行链：

```text
Agent
 ↓
Argument Validation
 ↓
Permission Check
 ↓
Risk Check
 ↓
Idempotency
 ↓
Timeout
 ↓
Rate Limit
 ↓
Audit Start
 ↓
Execute
 ↓
Audit Result
 ↓
Normalize Result
```

### 8.4 风险等级

| Level | 示例 | Approval |
|---|---|---|
| LOW | Prometheus Query | No |
| LOW | RabbitMQ Inspect | No |
| LOW | Runbook Search | No |
| MEDIUM | Restart Worker | Yes |
| HIGH | Scale Deployment | Yes |
| HIGH | Rollback | Yes |
| CRITICAL | Delete Queue | Always + forbidden in v1.0 |

---

## 9. 核心 Tool

### 9.1 PrometheusTool

能力：

```text
query
query_range
alerts
series
```

示例：

```json
{
  "query": "rate(http_requests_total{service=\"gocommunity\",status=~\"5..\"}[5m])"
}
```

### 9.2 RabbitMQTool

能力：

```text
queue_depth
consumer_count
message_rate
consumer_status
```

### 9.3 RunbookSearchTool

输入：

```json
{
  "query": "RabbitMQ backlog consumer connection refused"
}
```

返回：

```json
{
  "source": "rabbitmq-backlog.md",
  "heading": "消费者异常",
  "content": "...",
  "score": 0.91
}
```

### 9.4 IncidentHistoryTool

用于查询：

```text
同服务
同 Alert
同标签
同 Root Cause
```

### 9.5 RestartWorkerTool

v1.0 唯一自动处置 Tool。

执行前必须：

```text
Policy Check
Approval Check
Idempotency Check
```

执行后必须：

```text
Verify Consumer Count
Verify Queue Depth
Verify Error Rate
```

---

## 10. RAG / Knowledge System

### 10.1 数据源

```text
docs/
├── runbooks/
├── architecture/
├── incidents/
└── postmortems/
```

### 10.2 索引流程

```text
Document
  ↓
Parse
  ↓
Metadata
  ↓
Chunk
  ↓
Embedding
  ↓
Qdrant
```

### 10.3 Metadata

```json
{
  "source": "rabbitmq-backlog.md",
  "service": "GoCommunity",
  "type": "runbook",
  "severity": "P1",
  "tags": ["rabbitmq", "worker"]
}
```

### 10.4 检索策略

v1.0：

```text
Lexical Search
      +
Vector Search（候选集 CandidateK，如 20，而非全量 chunks）
      ↓
RRF Fusion
      ↓
TopK（如 8，由 rag.top_k 配置）
```

向量检索候选集大小由 `rag.candidate_k` 配置（默认 20），远小于知识库 chunk 总量，
避免每次查询都按整个知识库做向量 topK；最终返回条数由 `rag.top_k` 配置（默认 8）。

v1.1 再加入 Reranker。

### 10.5 Evidence 绑定

每次 Retriever 返回结果必须转换为 Evidence：

```text
Retriever Result
      ↓
EvidenceStore
      ↓
Agent Context
```

---

## 11. RabbitMQ 事件驱动

### 11.1 Exchange

```text
gooncall.events
```

Type：

```text
topic
```

### 11.2 Routing Key

```text
incident.created
incident.updated
incident.resolved
agent.run.started
agent.run.completed
agent.run.failed
tool.call.started
tool.call.completed
action.approval_required
action.approved
action.rejected
action.executed
```

### 11.3 Queue

```text
incident.queue
agent.queue
action.queue
audit.queue
notification.queue
```

### 11.4 Consumer

```text
API
 ↓
Publish Event
 ↓
RabbitMQ
 ↓
Worker
 ↓
Agent Runtime
```

### 11.5 幂等

Event Handler 使用：

```text
Redis SETNX
```

Key：

```text
gooncall:event:{event_id}
```

处理成功后：

```text
TTL 24h
```

---

## 12. Redis 设计

### 12.1 Key 规范

```text
gooncall:incident:fingerprint:{fingerprint}
gooncall:incident:{id}
gooncall:run:{id}
gooncall:session:{id}
gooncall:tool:idempotency:{key}
gooncall:event:{event_id}
gooncall:approval:{id}
```

### 12.2 用途

```text
Incident Dedup
Agent State
Session
Idempotency
Distributed Lock
Rate Limit
Temporary Checkpoint Metadata
```

### 12.3 Incident 去重

```text
Prometheus Alert
      ↓
Fingerprint
      ↓
Redis SETNX
      ↓
Existing?
 ├── Yes → Merge
 └── No  → Create Incident
```

---

## 13. PostgreSQL 数据库

### 13.1 核心表

```text
incidents
incident_events
agent_runs
agent_steps
agent_evidences
tool_calls
approvals
knowledge_documents
```

### 13.2 incidents

```sql
CREATE TABLE incidents (
    id VARCHAR(64) PRIMARY KEY,
    service VARCHAR(128) NOT NULL,
    severity VARCHAR(16) NOT NULL,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    alert_name VARCHAR(255),
    fingerprint VARCHAR(128),
    status VARCHAR(32) NOT NULL,
    started_at TIMESTAMP NOT NULL,
    resolved_at TIMESTAMP NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    INDEX idx_incident_service(service),
    INDEX idx_incident_status(status),
    INDEX idx_incident_fingerprint(fingerprint)
);
```

### 13.3 agent_runs

```sql
CREATE TABLE agent_runs (
    id VARCHAR(64) PRIMARY KEY,
    incident_id VARCHAR(64) NOT NULL,
    agent_type VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    goal TEXT,
    current_step INT DEFAULT 0,
    started_at TIMESTAMP NOT NULL,
    finished_at TIMESTAMP NULL,
    error TEXT,
    INDEX idx_agent_run_incident(incident_id)
);
```

其余表按同样原则设计，避免在 v1.0 引入过度复杂的事件溯源模型。

---

## 14. API 设计

### 14.1 Incident

```http
POST   /api/v1/incidents
GET    /api/v1/incidents
GET    /api/v1/incidents/:id
POST   /api/v1/incidents/:id/analyze
POST   /api/v1/incidents/:id/resolve
```

### 14.2 Agent Run

```http
GET /api/v1/runs/:id
GET /api/v1/runs/:id/steps
GET /api/v1/runs/:id/evidences
GET /api/v1/runs/:id/stream
```

### 14.3 Approval

```http
GET  /api/v1/approvals/:id
POST /api/v1/approvals/:id/approve
POST /api/v1/approvals/:id/reject
```

### 14.4 Knowledge

```http
POST /api/v1/knowledge/documents
POST /api/v1/knowledge/reindex
GET  /api/v1/knowledge/search
```

### 14.5 Health / Metrics

```http
GET /healthz
GET /readyz
GET /metrics
```

---

## 15. SSE 流式事件

`GET /api/v1/runs/:id/stream`

事件示例：

```text
event: run.started
data: {"run_id":"run_001"}

 event: agent.thinking
data: {"agent":"diagnosis"}

 event: tool.started
data: {"tool":"prometheus.query"}

 event: tool.completed
data: {"tool":"prometheus.query","duration_ms":32}

 event: evidence.created
data: {"type":"METRIC"}

 event: approval.required
data: {"approval_id":"ap_001"}

 event: action.completed
data: {"action":"restart_worker"}

 event: incident.resolved
data: {"incident_id":"inc_001"}

 event: done
data: {"run_id":"run_001"}
```

前端不需要理解 Agent 内部对象，只消费稳定事件协议。

---

## 16. Human-in-the-loop

### 16.1 审批流程

```text
Agent
 ↓
Action Tool
 ↓
Risk = MEDIUM
 ↓
Policy = RequireApproval
 ↓
Interrupt
 ↓
Persist Checkpoint
 ↓
SSE 推送 approval.required
 ↓
用户批准
 ↓
Resume
 ↓
Execute
```

Eino 官方 HITL 模式就是 Interrupt → Checkpoint → Resume，适合直接承担 Agent 的中断与恢复；业务审批记录仍由 GoOnCall 自己持久化。

### 16.2 审批接口

```json
POST /api/v1/approvals/ap_001/approve

{
  "comment": "确认重启 Worker"
}
```

然后：

```text
Approval Service
 ↓
Update Approval
 ↓
Publish action.approved
 ↓
Resume Agent Run
```

---

## 17. Incident 状态机

```text
              ┌────────────┐
              │    OPEN    │
              └─────┬──────┘
                    ↓
             INVESTIGATING
                    │
          ┌─────────┼─────────┐
          │                   │
          ↓                   ↓
    NEED_APPROVAL          RESOLVED
          │
          ↓
 WAITING_APPROVAL
      │         │
 approved     rejected
      │         │
      ↓         ↓
 MITIGATING   FAILED
      │
      ↓
  VERIFYING
      │
      ├── healthy → RESOLVED
      └── unhealthy → INVESTIGATING
```

状态转换必须由 Go Service 控制，不允许 LLM 直接修改状态。

---

## 18. Agent Prompt 体系

目录：

```text
prompts/
├── supervisor/
│   └── system.md
├── diagnosis/
│   └── system.md
├── action/
│   └── system.md
└── reviewer/
    └── system.md
```

### Supervisor 约束

```text
你是运维事件协调 Agent。
你不能直接执行高风险操作。
你必须优先收集证据，再生成操作建议。
所有事实必须来自 Tool Result 或 Evidence。
如果证据不足，继续调查，不允许猜测。
```

### Diagnosis 约束

```text
你负责故障定位，不负责生产环境修改。
每一个 Root Cause 判断必须至少关联一个 Evidence。
优先使用监控指标验证假设。
不能根据单条日志直接判定根因。
```

### Action 约束

```text
你负责把排障方案转换为结构化 Action。
任何写操作都必须检查 risk 和 approval_required。
禁止生成未注册 Tool。
禁止生成任意 Shell 指令。
```

### Reviewer 约束

```text
检查：
1. Evidence 是否充分
2. Root Cause 是否有支持
3. Action 是否对应 Root Cause
4. 风险是否正确
5. 验证方案是否存在
```

---

## 19. 典型故障 Demo

### 场景：GoCommunity RabbitMQ Consumer 异常

GoCommunity 已经具备 RabbitMQ Worker、Redis、Prometheus/Grafana 与异步任务，因此它可以直接作为 GoOnCall 的演示业务系统。

流程：

```text
GoCommunity Worker
      ↓
Consumer 数下降
      ↓
RabbitMQ Queue Depth 上升
      ↓
Prometheus Alert
      ↓
GoOnCall Incident
      ↓
Diagnosis Agent
```

Agent 查询：

```text
PrometheusTool
RabbitMQTool
RunbookSearchTool
IncidentHistoryTool
```

得到：

```text
Evidence #1
Queue depth = 2418

Evidence #2
Consumers = 2

Evidence #3
最近发布后出现 connection refused

Evidence #4
Runbook: rabbitmq-backlog.md
```

结论：

```text
Root Cause:
Worker consumer connection failure

Confidence:
0.91
```

建议：

```text
restart_worker
```

由于风险为 MEDIUM：

```text
WAITING_APPROVAL
```

用户批准后：

```text
Restart Worker
 ↓
Verify Consumers
 ↓
Verify Queue Depth
 ↓
Verify HTTP P95
 ↓
RESOLVED
```

---

## 20. Postmortem

Incident 解决后生成：

```markdown
# Incident INC-20260829-001

## Summary
GoCommunity RabbitMQ consumer 数量下降导致消息积压。

## Impact
Queue depth 从 120 上升到 2418。

## Root Cause
Worker 与 RabbitMQ 的连接异常。

## Evidence
- Consumer count: 8 → 2
- Queue depth: 120 → 2418
- Error log: connection refused

## Resolution
Restart worker deployment.

## Prevention
- 增加 consumer count 告警
- 增加 worker readiness 检查
- 发布后增加自动验证
```

Postmortem 写入：

```text
PostgreSQL
+
Qdrant Knowledge Base
```

为未来 Incident 提供检索数据。

---

## 21. 可观测性

### 21.1 Metrics

GoOnCall 自身至少提供：

```text
gooncall_incidents_total
gooncall_incident_resolution_seconds
gooncall_agent_runs_total
gooncall_agent_run_duration_seconds
gooncall_tool_calls_total
gooncall_tool_failures_total
gooncall_tool_duration_seconds
gooncall_rag_requests_total
gooncall_rag_latency_seconds
gooncall_approval_total
```

### 21.2 Labels

推荐：

```text
service
agent_type
tool
status
```

禁止将：

```text
incident_id
user_id
request_id
```

作为高基数 Prometheus Label。

### 21.3 Log

结构化 JSON：

```json
{
  "level": "INFO",
  "event": "tool.completed",
  "run_id": "run_001",
  "tool": "prometheus.query",
  "duration_ms": 31
}
```

### 21.4 Trace

v1.0 可以预留 OpenTelemetry：

```text
HTTP Request
  └── Agent Run
       ├── LLM Call
       ├── Tool Call
       ├── Retriever
       └── Database
```

---

## 22. 项目目录

```text
GoOnCallAgent/
├── cmd/
│   ├── api/
│   │   └── main.go
│   ├── worker/
│   │   └── main.go
│   └── mcp-server/
│       └── main.go
│
├── internal/
│   ├── api/
│   │   ├── handler/
│   │   ├── middleware/
│   │   ├── router/
│   │   └── dto/
│   │
│   ├── agent/
│   │   ├── runtime/
│   │   ├── supervisor/
│   │   ├── diagnosis/
│   │   ├── action/
│   │   ├── reviewer/
│   │   └── session/
│   │
│   ├── incident/
│   │   ├── model/
│   │   ├── service/
│   │   ├── repository/
│   │   └── state_machine/
│   │
│   ├── tool/
│   │   ├── registry/
│   │   ├── middleware/
│   │   ├── prometheus/
│   │   ├── rabbitmq/
│   │   ├── runbook/
│   │   ├── incident/
│   │   └── deployment/
│   │
│   ├── knowledge/
│   │   ├── loader/
│   │   ├── splitter/
│   │   ├── embedding/
│   │   ├── retriever/
│   │   └── vectorstore/
│   │
│   ├── execution/
│   │   ├── policy/
│   │   ├── approval/
│   │   ├── executor/
│   │   ├── retry/
│   │   └── verifier/
│   │
│   ├── messaging/
│   │   ├── producer/
│   │   ├── consumer/
│   │   └── events/
│   │
│   ├── storage/
│   │   ├── postgres/
│   │   ├── redis/
│   │   └── qdrant/
│   │
│   ├── observability/
│   │   ├── metrics/
│   │   ├── tracing/
│   │   └── logging/
│   │
│   └── config/
│
├── prompts/
├── migrations/
├── docs/
│   ├── architecture/
│   ├── runbooks/
│   ├── incidents/
│   └── postmortems/
├── deploy/
│   ├── docker/
│   └── k8s/
├── scripts/
├── tests/
│   ├── integration/
│   └── e2e/
├── docker-compose.yml
├── Makefile
├── go.mod
├── go.sum
└── README.md
```

---

## 23. 推荐依赖

基础依赖：

```text
github.com/gin-gonic/gin
github.com/cloudwego/eino
github.com/cloudwego/eino-ext
gorm.io/gorm
gorm.io/driver/postgres
github.com/redis/go-redis/v9
github.com/rabbitmq/amqp091-go
github.com/prometheus/client_golang
```

RAG：

```text
Qdrant Go Client
Embedding Provider
```

Observability：

```text
OpenTelemetry Go
```

测试：

```text
testing
httptest
miniredis
```

具体 Eino / Eino-Ext 版本以初始化项目时官方仓库当前稳定版本为准，不在设计文档中硬编码未来不可验证的版本号。

---

## 24. 配置文件

推荐：

```yaml
server:
  host: 0.0.0.0
  port: 8080

llm:
  provider: openai
  base_url: ${LLM_BASE_URL}
  api_key: ${LLM_API_KEY}
  model: ${LLM_MODEL}

postgres:
  dsn: ${POSTGRES_DSN}

redis:
  addr: ${REDIS_ADDR}

rabbitmq:
  url: ${RABBITMQ_URL}

prometheus:
  url: ${PROMETHEUS_URL}

qdrant:
  url: ${QDRANT_URL}
  collection: gooncall_knowledge

rag:
  top_k: 8
  candidate_k: 20

agent:
  max_steps: 15
  max_tool_calls: 20
  timeout_seconds: 180

approval:
  enabled: true
```

---

## 25. Docker Compose

v1.0 本地环境：

```text
docker-compose
├── gooncall-api
├── gooncall-worker
├── postgres
├── redis
├── rabbitmq
├── qdrant
├── prometheus
└── grafana
```

不要求本地直接部署 Kubernetes。

Kubernetes 作为 v1.1 Demo 环境。

---

## 26. 测试策略

### 26.1 Unit Test

重点：

```text
Incident State Machine
Tool Policy
Risk Evaluation
Redis Dedup
RabbitMQ Event Handler
Prompt Input Builder
```

### 26.2 Integration Test

```text
Gin
 + PostgreSQL
 + Redis
 + RabbitMQ
```

### 26.3 Agent Test

使用固定 Incident + Mock Tool：

```text
Input
 ↓
Agent
 ↓
Expected Tool Calls
 ↓
Expected Evidence
 ↓
Expected Conclusion
```

### 26.4 E2E

完整演示：

```text
Create Incident
 ↓
Analyze
 ↓
Prometheus Query
 ↓
RabbitMQ Query
 ↓
RAG
 ↓
Approval
 ↓
Action
 ↓
Verify
 ↓
Resolve
```

---

## 27. Agent 评估

v1.0 至少维护 20 个 Incident Case。

示例：

```text
case_001_p95_latency
case_002_error_rate
case_003_rabbitmq_backlog
case_004_redis_latency
case_005_worker_down
```

评估指标：

```text
Tool Selection Accuracy
Root Cause Accuracy
Evidence Coverage
Plan Validity
Approval Accuracy
Resolution Success Rate
Average Tool Calls
Average Agent Latency
```

重点不是追求 LLM 的“绝对准确”，而是衡量 Agent 是否能够在给定工具和知识条件下得到可验证结果。

---

## 28. 开发阶段

### Phase 1：基础骨架

```text
Go Project
Gin
Config
PostgreSQL
Redis
RabbitMQ
Docker Compose
```

完成：

```text
healthz
incidents CRUD
```

### Phase 2：Eino Agent

```text
LLM
Supervisor
Diagnosis
Tool Registry
```

完成：

```text
Incident → Agent → Tool → Result
```

### Phase 3：RAG

```text
Runbook Loader
Embedding
Qdrant
Retriever
Evidence
```

### Phase 4：Agent Runtime

```text
Agent Run
Agent Step
Tool Call
SSE
```

### Phase 5：HITL

```text
Risk Policy
Approval
Interrupt
Checkpoint
Resume
```

### Phase 6：自动处置

仅实现：

```text
restart_worker
```

然后：

```text
Execute → Verify → Resolve
```

### Phase 7：联动 GoCommunity

```text
GoCommunity
 ↓
Prometheus
 ↓
Alert
 ↓
GoOnCall
```

最终 Demo 完成。

---

## 29. v1.0 验收标准

### 功能

- [ ] 可以创建 Incident。
- [ ] 可以读取 Prometheus Alert。
- [ ] 可以运行 Eino Agent。
- [ ] Agent 可以使用 PrometheusTool。
- [ ] Agent 可以使用 RabbitMQTool。
- [ ] Agent 可以检索 Runbook。
- [ ] Agent 可以记录 Evidence。
- [ ] Agent Run 可以持久化。
- [ ] 支持 SSE。
- [ ] Action 可以进入审批。
- [ ] 审批后能够 Resume。
- [ ] Restart Worker 能执行。
- [ ] 执行后可以验证。
- [ ] Incident 可以自动关闭。
- [ ] 能够生成 Postmortem。

### 工程

- [ ] Redis 幂等。
- [ ] RabbitMQ 重试。
- [ ] Tool timeout。
- [ ] Tool audit。
- [ ] Prometheus metrics。
- [ ] Docker Compose 一键启动。
- [ ] Unit Test。
- [ ] Integration Test。
- [ ] E2E Demo。

---

## 30. v1.1 / v2.0 Roadmap

### v1.1

```text
Kubernetes Tool
Deployment Tool
Rollback Tool
OpenTelemetry
Reranker
Alertmanager Webhook
```

### v1.2

```text
Multi-Agent Supervisor
Parallel Diagnosis
Historical Incident Retrieval
Auto Postmortem
Notification
```

### v2.0

```text
Multi-Cluster
RBAC
Multi-Tenant
Agent Skill System
Policy-as-Code
Automatic Remediation
Knowledge Auto Update
Evaluation Platform
```

---

## 31. 简历项目定位

项目名称：

> GoOnCall Agent — 基于 Go + Eino 的 AIOps Agent Platform

推荐技术关键词：

```text
Go
Gin
Eino
Agent
ReAct
Workflow
Multi-Agent
RAG
Qdrant
MCP
RabbitMQ
Redis
PostgreSQL
Prometheus
Grafana
SSE
Docker
```

项目亮点应围绕：

```text
Agent Runtime
Tool Runtime
事件驱动
HITL
RAG
可观测性
故障闭环
```

不要把简历重点写成：

```text
“调用大模型生成排障建议”
```

而应该描述为：

```text
“构建 Go + Eino Agent Runtime，将 Incident、RAG、Tool Calling、审批、异步任务和自动验证串联为完整 AIOps 闭环。”
```

---

## 32. 与 GoCommunity 的最终联动架构

```text
                   GoCommunity
                       │
              ┌────────┴────────┐
              │                 │
            Business          Metrics
              │                 │
              │            Prometheus
              │                 │
              │               Alert
              │                 │
              └────────┬────────┘
                       ↓
                 GoOnCall Agent
                       │
                 Incident Engine
                       │
                 Eino Agent Runtime
                       │
          ┌────────────┼────────────┐
          │            │            │
         RAG       Prometheus    RabbitMQ
          │            │            │
          └────────────┼────────────┘
                       ↓
                  Root Cause
                       ↓
                    Action
                       ↓
                 HITL Approval
                       ↓
                  Go Worker / K8s
                       ↓
                    Verify
                       ↓
                   Resolved
                       ↓
                  Postmortem
                       ↓
                    Qdrant
```

---

## 33. 最终设计结论

GoOnCall Agent v1.0 的核心价值不是“又做了一个 Agent”，而是把 Agent 放入一个完整的 Go 后端工程体系中：

```text
Go Backend
   +
Agent Runtime
   +
Tool Runtime
   +
RAG
   +
Message Queue
   +
State Management
   +
Human Approval
   +
Observability
   +
Auto Remediation
```

最终形成：

```text
Detect
  ↓
Understand
  ↓
Plan
  ↓
Investigate
  ↓
Approve
  ↓
Execute
  ↓
Verify
  ↓
Learn
```

这套设计与现有 GoCommunity 的监控、RabbitMQ Worker、缓存和告警能力天然衔接，可以把两个项目组合成一个连续的工程故事：

> **GoCommunity 负责产生真实业务流量与故障信号；GoOnCall Agent 负责理解故障、执行排障和形成知识闭环。**

---

## 34. 参考资料

1. CloudWeGo Eino 官方仓库：https://github.com/cloudwego/eino
2. Eino 官方 Examples：https://github.com/cloudwego/eino-examples
3. Eino ADK / Human-in-the-loop：Eino 官方文档与示例
4. 现有项目 OnCallAgent：原项目 RAG、Prometheus、Plan-Execute-Replan、MCP、Evidence 设计
5. 现有项目 GoCommunity：Prometheus、RabbitMQ、Worker、Redis、Grafana 联动设计
