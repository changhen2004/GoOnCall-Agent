// Package store 使用 SQLite 实现新通用 Agent 服务的会话存储。
//
// 只服务 cmd/agent，不混入旧业务的 PostgreSQL 迁移。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

// DB 是会话存储。
type DB struct {
	db *sql.DB
}

// Open 打开数据库并执行迁移。
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("数据库路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite: %w", err)
	}
	// 单进程单写连接：简化 SQLite 并发语义；查询与写入都很短。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 SQLite: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("启用 WAL: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库: %w", err)
	}
	return &DB{db: db}, nil
}

// Close 关闭数据库。
func (d *DB) Close() error { return d.db.Close() }

// Lock 取得数据库对应的进程独占执行权。
//
// 使用操作系统文件锁，进程退出（包括崩溃）后自动释放；不以遗留标志文件或
// 运行时长判断旧进程是否存活。
func Lock(dbPath string) (io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}
	f, err := os.OpenFile(dbPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开进程锁: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("数据库 %s 已被另一个执行进程占用", dbPath)
	}
	return &fileLock{file: f}, nil
}

type fileLock struct {
	file *os.File
}

func (l *fileLock) Close() error {
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS conversations (
		id         TEXT PRIMARY KEY,
		owner      TEXT NOT NULL,
		owner_kind TEXT NOT NULL DEFAULT 'legacy',
		created_at INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS users (
		id            TEXT PRIMARY KEY,
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		enabled       INTEGER NOT NULL DEFAULT 1,
		created_at    INTEGER NOT NULL,
		updated_at    INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS login_sessions (
		token_hash TEXT PRIMARY KEY,
		user_id    TEXT NOT NULL REFERENCES users(id),
		expires_at INTEGER NOT NULL,
		created_at INTEGER NOT NULL,
		revoked_at INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS ix_login_sessions_user ON login_sessions(user_id, revoked_at)`,
	`CREATE TABLE IF NOT EXISTS ownership_migrations (
		id                 INTEGER PRIMARY KEY AUTOINCREMENT,
		legacy_actor       TEXT NOT NULL,
		user_id            TEXT NOT NULL REFERENCES users(id),
		conversation_count INTEGER NOT NULL,
		created_at         INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS runs (
		id              TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL REFERENCES conversations(id),
		model           TEXT NOT NULL,
		status          TEXT NOT NULL,
		budget_ms       INTEGER NOT NULL DEFAULT 0,
		reason          TEXT NOT NULL DEFAULT '',
		process_id      TEXT NOT NULL,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL
	)`,
	// 部分唯一索引由数据库保证同一会话最多一个活动运行。
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_runs_active
		ON runs(conversation_id) WHERE status IN ('running', 'waiting_approval')`,
	`CREATE INDEX IF NOT EXISTS ix_runs_conversation ON runs(conversation_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS messages (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT NOT NULL REFERENCES conversations(id),
		run_id          TEXT NOT NULL REFERENCES runs(id),
		seq             INTEGER NOT NULL,
		role            TEXT NOT NULL,
		content         TEXT NOT NULL DEFAULT '',
		tool_calls      TEXT NOT NULL DEFAULT '',
		tool_call_id    TEXT NOT NULL DEFAULT '',
		name            TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT 'complete',
		created_at      INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS ix_messages_conversation ON messages(conversation_id, seq)`,
	`CREATE TABLE IF NOT EXISTS tool_executions (
		id                TEXT PRIMARY KEY,
		run_id            TEXT NOT NULL REFERENCES runs(id),
		tool_call_id      TEXT NOT NULL,
		tool_name         TEXT NOT NULL,
		arguments         TEXT NOT NULL,
		requires_approval INTEGER NOT NULL DEFAULT 0,
		status            TEXT NOT NULL,
		result            TEXT NOT NULL DEFAULT '',
		created_at        INTEGER NOT NULL,
		updated_at        INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS ix_tool_executions_run ON tool_executions(run_id)`,
	`CREATE TABLE IF NOT EXISTS approvals (
		id                TEXT PRIMARY KEY,
		tool_execution_id TEXT NOT NULL UNIQUE REFERENCES tool_executions(id),
		run_id            TEXT NOT NULL REFERENCES runs(id),
		status            TEXT NOT NULL,
		operator          TEXT NOT NULL DEFAULT '',
		expires_at        INTEGER NOT NULL,
		created_at        INTEGER NOT NULL,
		updated_at        INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS ix_approvals_run ON approvals(run_id, status)`,
}

func migrate(ctx context.Context, db *sql.DB) error {
	for _, stmt := range schema {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	// 旧库升级：已有会话默认视为 legacy，必须显式迁移归属后新账号才能访问。
	return ensureColumn(ctx, db, "conversations", "owner_kind",
		`owner_kind TEXT NOT NULL DEFAULT 'legacy'`)
}

// ensureColumn 为旧数据库补充新列；SQLite 不支持 ADD COLUMN IF NOT EXISTS。
func ensureColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultV   sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultV, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+definition)
	return err
}
