-- GoOnCall Agent：Incident fingerprint 唯一索引。
--
-- 将去重指纹从普通索引改为 UNIQUE，解决高并发创建重复 Incident：
-- 并发请求同时通过 GetByFingerprint 的"不存在"检查后，只有一个能
-- INSERT 成功，另一个触发 unique_violation（SQLSTATE 23505），
-- 由上层按"已存在"返回已有记录。
DROP INDEX IF EXISTS idx_incident_fingerprint;
CREATE UNIQUE INDEX IF NOT EXISTS idx_incident_fingerprint ON incidents (fingerprint);
