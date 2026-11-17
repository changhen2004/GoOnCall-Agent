# GoOnCall Agent

> 通用运维 Agent：本地账号登录 → HTTP/SSE 会话 → 单 Agent 工具循环 → 只读 Prometheus / RabbitMQ / Runbook-RAG 工具 → 人工审批。

本仓库当前只保留通用 Agent 链路；旧 Incident 告警调度、Eino 诊断 Runtime、API/Worker、RabbitMQ 任务消息队列已移除。业务能力以**只读工具**形式接入，写操作仍走新服务的审批机制。

## 架构

```text
用户 → auth（密码登录 / 随机 Token 会话）
     → Agent HTTP API（Bearer 认证、SSE）
     → session（会话归属、运行协调、审批、生命周期）
     ├─ SQLite（用户、登录会话、会话、运行、消息、工具执行、审批、归属迁移审计）
     └─ Agent 核心（单次运行、工具循环、上下文预算）
         └─ 工具注册表
             ├─ Prometheus（指标 / 范围 / 告警，只读）
             ├─ RabbitMQ Management API（队列状态，只读）
             └─ Runbook/RAG（按需检索，返回来源与分数）
```

Agent 核心不依赖 auth、session、store、HTTP 或业务包。

## 快速开始

1. 准备配置：复制 `configs/config.example.env` 为 `configs/config.env`，填写模型、认证会话和需要启用的业务工具。真实配置文件不入库。
2. 创建账号（密码从标准输入读取）：

```bash
go run ./cmd/agentadmin create-user --username alice
```

3. 启动服务：

```bash
go run ./cmd/agent          # 默认 127.0.0.1:8091
# 或 make run-agent
```

4. 登录并调用：

```bash
BASE=http://127.0.0.1:8091
TOKEN=$(curl -s -X POST $BASE/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"<password>"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
AUTH="Authorization: Bearer $TOKEN"

CONV=$(curl -s -X POST $BASE/v1/conversations -H "$AUTH" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
RUN=$(curl -s -X POST $BASE/v1/conversations/$CONV/messages -H "$AUTH" \
  -H 'Content-Type: application/json' -d '{"content":"查看当前告警并检索 Runbook"}' \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["run_id"])')

curl -N "$BASE/v1/runs/$RUN/events" -H "$AUTH"     # SSE 实时事件
curl "$BASE/v1/runs/$RUN/approvals" -H "$AUTH"    # 待审批工具与完整参数
curl -X POST "$BASE/v1/runs/$RUN/cancel" -H "$AUTH"
```

也可使用 `AGENT_USERNAME=alice AGENT_PASSWORD='...' ./scripts/demo.sh`。

## HTTP 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/auth/login` | 登录，返回一次性原始 Token |
| POST | `/v1/auth/logout` | 撤销当前 Token；重复调用幂等 |
| GET | `/v1/me` | 当前用户与会话到期时间 |
| POST | `/v1/conversations` | 创建会话 |
| GET | `/v1/conversations/{id}` | 查询会话、消息与运行 |
| POST | `/v1/conversations/{id}/messages` | 提交消息，后台启动运行 |
| GET | `/v1/runs/{id}` | 查询运行、工具执行与待审批项 |
| GET | `/v1/runs/{id}/events` | SSE 实时事件（断连不影响执行） |
| POST | `/v1/runs/{id}/cancel` | 主动取消运行 |
| GET | `/v1/runs/{id}/approvals` | 查询待审批项 |
| POST | `/v1/approvals/{id}/decision` | `{"decision":"approve"\|"reject"}` |

所有会话、运行、SSE、取消与审批接口都会校验会话归属；Token 无效、过期、已撤销或用户禁用时返回 401。

## 账号与会话

- 密码使用 bcrypt；数据库只保存密码哈希。
- 登录 Token 由密码学安全随机数生成，数据库只保存 SHA-256 摘要。
- 默认会话有效期 24 小时，固定有效期，不续期、不刷新。
- 多设备登录相互独立；普通退出只撤销当前 Token。
- 重置密码撤销该用户全部 Token；禁用用户撤销全部 Token，并由运行中服务在 `user_status_poll_seconds` 周期内取消其活动运行。
- 登录失败限制为有界内存计数（容量、窗口、次数均可配置），未知账号与错误密码返回相同响应。

管理命令：

```bash
go run ./cmd/agentadmin create-user  --username alice
go run ./cmd/agentadmin disable-user --user alice
go run ./cmd/agentadmin enable-user  --user alice
go run ./cmd/agentadmin reset-password --user alice
go run ./cmd/agentadmin migrate-owner --legacy-actor old-admin --user-id <uuid>
```

`agentadmin` 只写 SQLite，不取得执行进程锁，可与服务同时运行；密码只从标准输入读取。

## 旧身份迁移

旧版本使用固定 Token 操作者字符串，新会话使用稳定用户 ID，并用 `conversations.owner_kind` 区分：

- 旧会话默认 `owner_kind=legacy`，新账号不会自动继承，同名账号也不会。
- 先备份数据库，再显式迁移：

```bash
# 建议先停止服务，或使用 SQLite 在线备份
sqlite3 data/agent.db ".backup 'data/agent.db.bak'"
go run ./cmd/agentadmin migrate-owner --legacy-actor old-admin --user-id <uuid>
```

迁移在事务中更新会话归属并写入 `ownership_migrations` 审计记录；重复执行没有可迁移记录时返回 0。消息、运行、工具记录的内容不变。代码回退不能撤销已执行的数据迁移。

## 业务工具

工具必须显式启用；未启用时不注册、不要求配置。启用但配置缺失会在启动时失败；外部服务暂不可达不会阻止登录和其他能力，调用时返回明确错误。

```yaml
prometheus:
  enabled: true
  url: ${PROMETHEUS_URL}

rabbitmq:
  enabled: true
  management_url: ${RABBITMQ_MANAGEMENT_URL}
  username: ${RABBITMQ_USERNAME}
  password: ${RABBITMQ_PASSWORD}

rag:
  enabled: true
  docs_dir: docs
  top_k: 8
  candidate_k: 20
```

- `prometheus_query` / `prometheus_range_query` / `prometheus_alerts`
- `rabbitmq_queue_status`
- `knowledge_search`（Runbook 混合检索）

限制：单次工具超时 `tool.timeout_seconds`；范围查询最多 24 小时、每序列最多 100 点；结果数量与字节数有上限并返回 `truncated` 标识；RAG 内容截断带 `content_truncated`。工具参数只包含查询条件，URL、凭据和资源选择由服务端配置，模型不能指定任意地址。

示例工具默认关闭；本地验证可设置 `agent_service.demo_tools_enabled: true`，它只操作临时目录。

## 知识导入与评测

```bash
# 导入 docs/runbooks 到 Qdrant（需要 rag.enabled=true）
go run ./cmd/knowledgeimport

# 运行文档级检索评测，保留 Recall@k、MRR、NDCG@5 与分组件报告
go run ./cmd/rageval -qrels docs/eval/runbook_qrels.jsonl
```

真实 Embedding、Qdrant、重排服务未配置时不会执行这两类联调。

## 配置

- `configs/config.go`：结构、加载与校验。
- `configs/config.env`：本机真实配置（不入库）。
- `configs/config.example.env`：脱敏模板。

覆盖顺序：文件值 → `${ENV}` 展开 → `AGENT_*` / 业务服务环境变量 → 内置默认值。模型 API Key 可用 `api_key` 或 `api_key_env`。日志与接口不输出密钥。

## 验证

```bash
go build ./...
go test ./... -count=1
go vet ./...
go test -race ./internal/auth/... ./internal/session/... ./internal/api/agent/... -count=1
```

单元测试使用 `httptest` 覆盖认证、登录限制、跨用户隔离、审批、SQLite、SSE 断连、上下文裁剪、工具截断与 RAG 懒加载；真实模型、Prometheus、RabbitMQ、Qdrant 需单独联调，不把模拟测试称为真实接入验证。
