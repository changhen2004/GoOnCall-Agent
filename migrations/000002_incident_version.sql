-- GoOnCall Agent：Incident 乐观锁（version CAS）。
--
-- 并发状态迁移保护：UPDATE ... WHERE id = ? AND version = ?，
-- RowsAffected == 0 即判定为并发冲突（state changed concurrently）。
-- 新建记录默认 version = 0，每次成功更新自增 1。
ALTER TABLE incidents ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 0;
