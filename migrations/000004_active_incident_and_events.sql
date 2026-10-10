-- GoOnCall Agent：活动 Incident / 活动 Run 的"仅非终态唯一"约束。
--
-- 背景：
--   1) 旧约束 idx_incident_fingerprint 对 fingerprint 全局唯一，导致终态
--      （RESOLVED/FAILED/CANCELLED）之后同指纹告警复发无法创建新 Incident。
--      改为部分唯一索引：同一 fingerprint 只允许一条非终态记录。
--   2) agent_runs 之前没有任何约束，重复消息会为一个 Incident 创建多个 Run。
--      增加部分唯一索引：同一 Incident 最多一条 RUNNING/COMPLETED Run；
--      Run 进入 FAILED/INTERRUPTED 后允许重试创建新 Run。
--
-- 注意：索引 WHERE 谓词必须与 Go 侧保持一致：
--   model.Incident.Fingerprint / model.AgentRun.IncidentID 的 GORM 标签，
--   以及 repository.activeIncidentPredicate。
--   谓词写成 <> 串联（而非 NOT IN (...)）是因为 GORM 索引标签以逗号分隔参数。

-- 1) Incident：全局唯一 -> 仅非终态唯一。
DROP INDEX IF EXISTS idx_incident_fingerprint;
CREATE UNIQUE INDEX IF NOT EXISTS idx_incident_active_fingerprint
    ON incidents (fingerprint)
    WHERE status <> 'RESOLVED' AND status <> 'FAILED' AND status <> 'CANCELLED';

-- 2) 历史数据清理：旧版本没有活动 Run 约束，同一 Incident 可能残留多条
--    RUNNING/COMPLETED Run，直接建唯一索引会失败。保留每组最新一条，
--    其余标为 INTERRUPTED（不删除记录）。
UPDATE agent_runs r SET status = 'INTERRUPTED'
WHERE r.status IN ('RUNNING', 'COMPLETED')
  AND EXISTS (
    SELECT 1 FROM agent_runs n
    WHERE n.incident_id = r.incident_id
      AND n.status IN ('RUNNING', 'COMPLETED')
      AND (n.started_at, n.id) > (r.started_at, r.id)
  );

-- 3) Run：同一 Incident 至多一条活动 Run。
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_run_active
    ON agent_runs (incident_id)
    WHERE status <> 'FAILED' AND status <> 'INTERRUPTED';
