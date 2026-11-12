package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gooncall-agent/internal/auth"
	"gooncall-agent/internal/session"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func createTestUser(t *testing.T, db *DB, username string) *auth.User {
	t.Helper()
	now := time.Now()
	user := &auth.User{
		ID: "user-" + username, Username: username, PasswordHash: "hash",
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.CreateUser(context.Background(), *user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

func TestAuthStoreDisableRevokesAndRejectsLoginSessions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now()
	user := createTestUser(t, db, "alice")

	if err := db.CreateUser(ctx, auth.User{
		ID: "other", Username: "alice", PasswordHash: "hash", Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatalf("重复用户名错误 = %v", err)
	}

	sessionRecord := auth.LoginSession{
		TokenHash: auth.TokenHash("token-1"), UserID: user.ID,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	if err := db.CreateLoginSession(ctx, sessionRecord); err != nil {
		t.Fatalf("CreateLoginSession: %v", err)
	}

	disabled, err := db.DisableUser(ctx, user.ID, now)
	if err != nil || !disabled {
		t.Fatalf("DisableUser = %v, %v", disabled, err)
	}
	revoked, err := db.LoginSessionByHash(ctx, auth.TokenHash("token-1"))
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("禁用后会话未撤销: %+v, %v", revoked, err)
	}
	if err := db.CreateLoginSession(ctx, auth.LoginSession{
		TokenHash: auth.TokenHash("token-2"), UserID: user.ID,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}); !errors.Is(err, auth.ErrUserDisabled) {
		t.Fatalf("禁用后创建会话错误 = %v", err)
	}

	enabled, err := db.EnableUser(ctx, user.ID, now)
	if err != nil || !enabled {
		t.Fatalf("EnableUser = %v, %v", enabled, err)
	}
	stillRevoked, _ := db.LoginSessionByHash(ctx, auth.TokenHash("token-1"))
	if stillRevoked.RevokedAt == nil {
		t.Fatal("重新启用不应恢复旧 Token")
	}

	if err := db.CreateLoginSession(ctx, auth.LoginSession{
		TokenHash: auth.TokenHash("token-3"), UserID: user.ID,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}); err != nil {
		t.Fatalf("重新启用后创建会话: %v", err)
	}
	if changed, err := db.ResetUserPassword(ctx, user.ID, "new-hash", now); err != nil || !changed {
		t.Fatalf("ResetUserPassword = %v, %v", changed, err)
	}
	afterReset, _ := db.LoginSessionByHash(ctx, auth.TokenHash("token-3"))
	if afterReset.RevokedAt == nil {
		t.Fatal("重置密码应撤销全部 Token")
	}
}

func TestMigrateLegacyOwner(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now()
	user := createTestUser(t, db, "alice")

	_, err := db.db.ExecContext(ctx,
		`INSERT INTO conversations (id, owner, owner_kind, created_at) VALUES (?, ?, 'legacy', ?)`,
		"legacy-conv", "old-token-actor", now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}

	count, err := db.MigrateLegacyOwner(ctx, "old-token-actor", user.ID, now)
	if err != nil || count != 1 {
		t.Fatalf("MigrateLegacyOwner = %d, %v", count, err)
	}
	conversation, err := db.Conversation(ctx, "legacy-conv")
	if err != nil {
		t.Fatal(err)
	}
	if conversation.Owner != user.ID || conversation.OwnerKind != session.OwnerKindUser {
		t.Fatalf("迁移后归属 = %+v", conversation)
	}

	if again, err := db.MigrateLegacyOwner(ctx, "old-token-actor", user.ID, now); err != nil || again != 0 {
		t.Fatalf("重复迁移 = %d, %v", again, err)
	}

	var auditCount int
	if err := db.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ownership_migrations WHERE legacy_actor = ? AND user_id = ?`,
		"old-token-actor", user.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("审计记录数 = %d", auditCount)
	}

	if _, err := db.MigrateLegacyOwner(ctx, "old-token-actor", "missing-user", now); err == nil {
		t.Fatal("目标用户不存在时应失败")
	}
}
