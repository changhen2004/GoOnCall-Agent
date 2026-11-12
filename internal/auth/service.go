package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Options 是认证服务配置。
type Options struct {
	SessionTTL           time.Duration
	LoginMaxAttempts     int
	LoginWindow          time.Duration
	LoginLimiterCapacity int
	// Now 可在测试中注入固定时间；为空时使用 time.Now。
	Now func() time.Time
}

// Service 处理登录、Token 认证与退出。
type Service struct {
	store     Store
	opts      Options
	limiter   *loginLimiter
	now       func() time.Time
	dummyHash string
}

// New 创建认证服务。
func New(store Store, opts Options) (*Service, error) {
	if store == nil {
		return nil, errors.New("认证存储不能为空")
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 24 * time.Hour
	}
	if opts.LoginMaxAttempts <= 0 {
		opts.LoginMaxAttempts = 5
	}
	if opts.LoginWindow <= 0 {
		opts.LoginWindow = 5 * time.Minute
	}
	if opts.LoginLimiterCapacity <= 0 {
		opts.LoginLimiterCapacity = 1024
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	dummyHash, err := HashPassword("gooncall-dummy-password-for-timing")
	if err != nil {
		return nil, fmt.Errorf("初始化密码校验: %w", err)
	}

	return &Service{
		store:     store,
		opts:      opts,
		limiter:   newLoginLimiter(opts.LoginLimiterCapacity, opts.LoginMaxAttempts, opts.LoginWindow),
		now:       opts.Now,
		dummyHash: dummyHash,
	}, nil
}

// Login 校验密码并创建独立登录会话。失败响应不区分账号不存在、密码错误或账号禁用。
func (s *Service) Login(ctx context.Context, username, password, limitKey string) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	if limitKey == "" {
		limitKey = "unknown"
	}
	key := limitKey + "|" + strings.ToLower(username)
	now := s.now()

	if !s.limiter.allow(key, now) {
		return nil, ErrTooManyAttempts
	}

	user, err := s.store.UserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_ = CheckPassword(password, s.dummyHash)
			s.limiter.fail(key, now)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	passwordOK := CheckPassword(password, user.PasswordHash)
	if !user.Enabled || !passwordOK {
		s.limiter.fail(key, now)
		return nil, ErrInvalidCredentials
	}

	token, err := generateToken()
	if err != nil {
		return nil, err
	}
	session := LoginSession{
		TokenHash: TokenHash(token),
		UserID:    user.ID,
		ExpiresAt: now.Add(s.opts.SessionTTL),
		CreatedAt: now,
	}
	if err := s.store.CreateLoginSession(ctx, session); err != nil {
		if errors.Is(err, ErrUserDisabled) {
			s.limiter.fail(key, now)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	s.limiter.reset(key)
	return &LoginResult{Token: token, ExpiresAt: session.ExpiresAt, User: *user}, nil
}

// Authenticate 每次请求都检查 Token、有效期、撤销状态和用户启用状态。
func (s *Service) Authenticate(ctx context.Context, token string) (*Identity, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrUnauthenticated
	}

	session, err := s.store.LoginSessionByHash(ctx, TokenHash(token))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrUnauthenticated
		}
		return nil, err
	}
	if session.RevokedAt != nil || !s.now().Before(session.ExpiresAt) {
		return nil, ErrUnauthenticated
	}

	user, err := s.store.UserByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrUnauthenticated
		}
		return nil, err
	}
	if !user.Enabled {
		return nil, ErrUnauthenticated
	}

	return &Identity{UserID: user.ID, Username: user.Username, ExpiresAt: session.ExpiresAt}, nil
}

// Logout 只撤销当前 Token，重复调用保持幂等。
func (s *Service) Logout(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	return s.store.RevokeLoginSession(ctx, TokenHash(token), s.now())
}

// TokenHash 返回 Token 的 SHA-256 十六进制摘要；数据库只保存该摘要。
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成登录 Token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
