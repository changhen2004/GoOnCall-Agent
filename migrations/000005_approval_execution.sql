-- GoOnCall Agent：Approval 执行审计字段与状态扩展。
--
-- 目的：
--   * IncidentID 固化审批归属的 Incident（创建时由 Run 解析），外部恢复/重启后仍可判断；
--   * Result / Error / OperationID 记录真实处置结果，供审计与人工核对；
--   * UpdatedAt 支持按最近变更排序与外部恢复过期判定。
--
-- 新增状态（无需 DDL，status 是 VARCHAR(32)）：EXPIRED、UNKNOWN。
--   EXPIRED：Incident 已外部恢复或不再允许执行；
--   UNKNOWN：外部动作超时，无法确认是否已执行，禁止自动重试。

ALTER TABLE approvals ADD COLUMN IF NOT EXISTS incident_id VARCHAR(64);
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS result TEXT;
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS error TEXT;
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS operation_id VARCHAR(255);
-- 000001 建表时漏了审批入参列，这里一并补齐。
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS arguments TEXT;
-- 历史行没有 updated_at；带默认值可避免 NULL 扫描失败（Go 侧字段是 time.Time）。
ALTER TABLE approvals ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS idx_approval_incident ON approvals (incident_id);

-- 历史审批回填 incident_id：000005 之前的审批没有该列，外部恢复调用
-- ExpirePendingByIncident 时匹配不到它们，恢复之后旧审批仍可能执行旧处置。
-- 通过 run_id -> agent_runs.incident_id 回填；找不到 Run 的孤儿审批保持 NULL
-- （不会误配到任何 Incident，也不会被错误过期）。
UPDATE approvals a SET incident_id = r.incident_id
FROM agent_runs r
WHERE a.incident_id IS NULL AND a.run_id = r.id;

-- 历史遗留的 APPROVED / EXECUTING 审批不在迁移中自动改写：
-- 它们代表"可能已经执行过"的外部动作，需要运维核对。
