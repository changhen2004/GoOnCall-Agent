package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gooncall-agent/internal/auth"
)

// CreateUser 保存新账号。
func (d *DB) CreateUser(ctx context.Context, user auth.User) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, boolInt(user.Enabled),
		millis(user.CreatedAt), millis(user.UpdatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return auth.ErrUsernameTaken
		}
		return fmt.Errorf("创建用户: %w", err)
	}
	return nil
}

// UserByUsername 按用户名查询账号。
func (d *DB) UserByUsername(ctx context.Context, username string) (*auth.User, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, enabled, created_at, updated_at
		 FROM users WHERE username = ?`, username)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户: %w", err)
	}
	return user, nil
}

// UserByID 按稳定用户 ID 查询账号。
func (d *DB) UserByID(ctx context.Context, id string) (*auth.User, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, enabled, created_at, updated_at
		 FROM users WHERE id = ?`, id)
	user, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户: %w", err)
	}
	return user, nil
}

// CreateLoginSession 仅在用户仍启用时创建会话，避免禁用与登录之间的竞争。
func (d *DB) CreateLoginSession(ctx context.Context, session auth.LoginSession) error {
	result, err := d.db.ExecContext(ctx,
		`INSERT INTO login_sessions (token_hash, user_id, expires_at, created_at)
		 SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM users WHERE id = ? AND enabled = 1)`,
		session.TokenHash, session.UserID, session.ExpiresAt.UnixMilli(),
		millis(session.CreatedAt), session.UserID)
	if err != nil {
		return fmt.Errorf("创建登录会话: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return auth.ErrUserDisabled
	}
	return nil
}

// LoginSessionByHash 按 Token 摘要查询登录会话。
func (d *DB) LoginSessionByHash(ctx context.Context, tokenHash string) (*auth.LoginSession, error) {
	var (
		session   auth.LoginSession
		expires   int64
		created   int64
		revokedAt sql.NullInt64
	)
	err := d.db.QueryRowContext(ctx,
		`SELECT token_hash, user_id, expires_at, created_at, revoked_at
		 FROM login_sessions WHERE token_hash = ?`, tokenHash).
		Scan(&session.TokenHash, &session.UserID, &expires, &created, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, auth.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询登录会话: %w", err)
	}
	session.ExpiresAt = fromMillis(expires)
	session.CreatedAt = fromMillis(created)
	if revokedAt.Valid {
		t := fromMillis(revokedAt.Int64)
		session.RevokedAt = &t
	}
	return &session, nil
}

// RevokeLoginSession 撤销单个 Token；重复撤销保持幂等。
func (d *DB) RevokeLoginSession(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE login_sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL`,
		now.UnixMilli(), tokenHash)
	if err != nil {
		return fmt.Errorf("撤销登录会话: %w", err)
	}
	return nil
}

// RevokeAllSessions 撤销用户全部 Token。
func (d *DB) RevokeAllSessions(ctx context.Context, userID string, now time.Time) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE login_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now.UnixMilli(), userID)
	if err != nil {
		return fmt.Errorf("撤销用户登录会话: %w", err)
	}
	return nil
}

// DisableUser 禁用用户并在同一事务中撤销其全部 Token。
func (d *DB) DisableUser(ctx context.Context, userID string, now time.Time) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`UPDATE users SET enabled = 0, updated_at = ? WHERE id = ? AND enabled = 1`,
		now.UnixMilli(), userID)
	if err != nil {
		return false, fmt.Errorf("禁用用户: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE login_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now.UnixMilli(), userID); err != nil {
		return false, fmt.Errorf("撤销用户登录会话: %w", err)
	}
	return n > 0, tx.Commit()
}

// EnableUser 重新启用用户；旧 Token 不会恢复。
func (d *DB) EnableUser(ctx context.Context, userID string, now time.Time) (bool, error) {
	result, err := d.db.ExecContext(ctx,
		`UPDATE users SET enabled = 1, updated_at = ? WHERE id = ? AND enabled = 0`,
		now.UnixMilli(), userID)
	if err != nil {
		return false, fmt.Errorf("启用用户: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// ResetUserPassword 更新密码并撤销该用户全部 Token。
func (d *DB) ResetUserPassword(ctx context.Context, userID, passwordHash string, now time.Time) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, now.UnixMilli(), userID)
	if err != nil {
		return false, fmt.Errorf("重置密码: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE login_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
		now.UnixMilli(), userID); err != nil {
		return false, fmt.Errorf("撤销用户登录会话: %w", err)
	}
	return n > 0, tx.Commit()
}

// DisabledUserIDs 返回当前禁用用户，供运行中服务发现需要取消的运行。
func (d *DB) DisabledUserIDs(ctx context.Context) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT id FROM users WHERE enabled = 0`)
	if err != nil {
		return nil, fmt.Errorf("查询禁用用户: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MigrateLegacyOwner 将指定旧操作者身份的会话显式迁移到新用户 ID。
//
// 只更新 conversations.owner/owner_kind 并写入审计记录；消息、运行和工具记录
// 保留原始事实。重复执行没有可迁移记录时返回 0。
func (d *DB) MigrateLegacyOwner(ctx context.Context, legacyActor, userID string, now time.Time) (int64, error) {
	if strings.TrimSpace(legacyActor) == "" || strings.TrimSpace(userID) == "" {
		return 0, errors.New("legacy actor 和 user id 不能为空")
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, fmt.Errorf("目标用户 %s 不存在", userID)
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE conversations SET owner = ?, owner_kind = 'user'
		 WHERE owner = ? AND owner_kind = 'legacy'`, userID, legacyActor)
	if err != nil {
		return 0, fmt.Errorf("迁移会话归属: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if count > 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ownership_migrations (legacy_actor, user_id, conversation_count, created_at)
			 VALUES (?, ?, ?, ?)`, legacyActor, userID, count, now.UnixMilli()); err != nil {
			return 0, fmt.Errorf("写入迁移审计: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func scanUser(row rowScanner) (*auth.User, error) {
	var user auth.User
	var enabled int
	var created, updated int64
	err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &enabled, &created, &updated)
	if err != nil {
		return nil, err
	}
	user.Enabled = enabled != 0
	user.CreatedAt = fromMillis(created)
	user.UpdatedAt = fromMillis(updated)
	return &user, nil
}
