-- GoOnCall Agent v1.0 初始表结构（对应设计文档第 5 / 13 节）。

-- 事件（Incident）
CREATE TABLE IF NOT EXISTS incidents (
    id          VARCHAR(64)  PRIMARY KEY,
    service     VARCHAR(128) NOT NULL,
    severity    VARCHAR(16)  NOT NULL,
    title       VARCHAR(255) NOT NULL,
    description TEXT,
    alert_name  VARCHAR(255),
    fingerprint VARCHAR(128),
    status      VARCHAR(32)  NOT NULL,
    started_at  TIMESTAMP    NOT NULL,
    resolved_at TIMESTAMP    NULL,
    created_at  TIMESTAMP    NOT NULL,
    updated_at  TIMESTAMP    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_incident_service     ON incidents (service);
CREATE INDEX IF NOT EXISTS idx_incident_status      ON incidents (status);
CREATE INDEX IF NOT EXISTS idx_incident_fingerprint ON incidents (fingerprint);

-- Incident 生命周期事件（审计 / 事件溯源）
CREATE TABLE IF NOT EXISTS incident_events (
    id          VARCHAR(64) PRIMARY KEY,
    incident_id VARCHAR(64) NOT NULL,
    event_type  VARCHAR(64) NOT NULL,
    payload     JSONB,
    created_at  TIMESTAMP   NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_incident_event_incident ON incident_events (incident_id);

-- Agent 运行
CREATE TABLE IF NOT EXISTS agent_runs (
    id           VARCHAR(64) PRIMARY KEY,
    incident_id  VARCHAR(64) NOT NULL,
    agent_type   VARCHAR(64) NOT NULL,
    status       VARCHAR(32) NOT NULL,
    goal         TEXT,
    current_step INT DEFAULT 0,
    started_at   TIMESTAMP   NOT NULL,
    finished_at  TIMESTAMP   NULL,
    error        TEXT
);
CREATE INDEX IF NOT EXISTS idx_agent_run_incident ON agent_runs (incident_id);

-- Agent 步骤
CREATE TABLE IF NOT EXISTS agent_steps (
    id         VARCHAR(64) PRIMARY KEY,
    run_id     VARCHAR(64) NOT NULL,
    step_index INT         NOT NULL,
    agent      VARCHAR(64) NOT NULL,
    action     VARCHAR(128) NOT NULL,
    status     VARCHAR(32) NOT NULL,
    input      TEXT,
    output     TEXT,
    duration   BIGINT DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_agent_step_run ON agent_steps (run_id);

-- Agent 证据
CREATE TABLE IF NOT EXISTS agent_evidences (
    id         VARCHAR(64) PRIMARY KEY,
    run_id     VARCHAR(64) NOT NULL,
    type       VARCHAR(32) NOT NULL,
    source     VARCHAR(255),
    content    TEXT,
    metadata   JSONB,
    created_at TIMESTAMP   NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_evidence_run ON agent_evidences (run_id);

-- 工具调用
CREATE TABLE IF NOT EXISTS tool_calls (
    id          VARCHAR(64) PRIMARY KEY,
    run_id      VARCHAR(64) NOT NULL,
    tool_name   VARCHAR(128) NOT NULL,
    risk_level  VARCHAR(16) NOT NULL,
    arguments   TEXT,
    result      TEXT,
    status      VARCHAR(32) NOT NULL,
    duration_ms BIGINT DEFAULT 0,
    created_at  TIMESTAMP   NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tool_call_run ON tool_calls (run_id);

-- 人工审批
CREATE TABLE IF NOT EXISTS approvals (
    id          VARCHAR(64) PRIMARY KEY,
    run_id      VARCHAR(64) NOT NULL,
    tool_call_id VARCHAR(64) NOT NULL,
    action      VARCHAR(128) NOT NULL,
    reason      TEXT,
    status      VARCHAR(32) NOT NULL,
    approved_by VARCHAR(128),
    created_at  TIMESTAMP   NOT NULL,
    approved_at TIMESTAMP   NULL
);
CREATE INDEX IF NOT EXISTS idx_approval_run ON approvals (run_id);

-- 知识文档（Runbook / Postmortem / Incident 沉淀）
CREATE TABLE IF NOT EXISTS knowledge_documents (
    id          VARCHAR(64) PRIMARY KEY,
    source      VARCHAR(255) NOT NULL,
    doc_type    VARCHAR(32)  NOT NULL,
    service     VARCHAR(128),
    title       VARCHAR(255),
    content     TEXT,
    chunk_index INT DEFAULT 0,
    vector_id   VARCHAR(128),
    created_at  TIMESTAMP    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_knowledge_doc_type ON knowledge_documents (doc_type);
