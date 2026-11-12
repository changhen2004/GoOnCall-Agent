package auth

import (
	"context"
	"time"
)

// Store 是认证层需要的持久化契约。
type Store interface {
	CreateUser(ctx context.Context, user User) error
	UserByUsername(ctx context.Context, username string) (*User, error)
	UserByID(ctx context.Context, id string) (*User, error)
	// CreateLoginSession 仅在用户仍处于启用状态时创建会话；否则返回 ErrUserDisabled。
	CreateLoginSession(ctx context.Context, session LoginSession) error
	LoginSessionByHash(ctx context.Context, tokenHash string) (*LoginSession, error)
	RevokeLoginSession(ctx context.Context, tokenHash string, now time.Time) error
	RevokeAllSessions(ctx context.Context, userID string, now time.Time) error
}
