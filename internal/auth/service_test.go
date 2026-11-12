package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryStore struct {
	users    map[string]*User
	byName   map[string]*User
	sessions map[string]*LoginSession
	now      func() time.Time
}

func newMemoryStore(now func() time.Time) *memoryStore {
	return &memoryStore{
		users:    make(map[string]*User),
		byName:   make(map[string]*User),
		sessions: make(map[string]*LoginSession),
		now:      now,
	}
}

func (m *memoryStore) CreateUser(_ context.Context, user User) error {
	if _, ok := m.byName[user.Username]; ok {
		return ErrUsernameTaken
	}
	cp := user
	m.users[user.ID] = &cp
	m.byName[user.Username] = &cp
	return nil
}

func (m *memoryStore) UserByUsername(_ context.Context, username string) (*User, error) {
	if user, ok := m.byName[username]; ok {
		return user, nil
	}
	return nil, ErrNotFound
}

func (m *memoryStore) UserByID(_ context.Context, id string) (*User, error) {
	if user, ok := m.users[id]; ok {
		return user, nil
	}
	return nil, ErrNotFound
}

func (m *memoryStore) CreateLoginSession(_ context.Context, session LoginSession) error {
	user, ok := m.users[session.UserID]
	if !ok || !user.Enabled {
		return ErrUserDisabled
	}
	cp := session
	m.sessions[session.TokenHash] = &cp
	return nil
}

func (m *memoryStore) LoginSessionByHash(_ context.Context, hash string) (*LoginSession, error) {
	if session, ok := m.sessions[hash]; ok {
		return session, nil
	}
	return nil, ErrNotFound
}

func (m *memoryStore) RevokeLoginSession(_ context.Context, hash string, now time.Time) error {
	if session, ok := m.sessions[hash]; ok {
		session.RevokedAt = &now
	}
	return nil
}

func (m *memoryStore) RevokeAllSessions(_ context.Context, userID string, now time.Time) error {
	for _, session := range m.sessions {
		if session.UserID == userID {
			session.RevokedAt = &now
		}
	}
	return nil
}

func newTestService(t *testing.T, now func() time.Time) (*Service, *memoryStore, *User) {
	t.Helper()
	store := newMemoryStore(now)
	hash, err := HashPassword("secret-password")
	if err != nil {
		t.Fatal(err)
	}
	user := &User{
		ID: "u1", Username: "alice", PasswordHash: hash, Enabled: true,
		CreatedAt: now(), UpdatedAt: now(),
	}
	if err := store.CreateUser(context.Background(), *user); err != nil {
		t.Fatal(err)
	}
	service, err := New(store, Options{
		SessionTTL: time.Hour, LoginMaxAttempts: 3, LoginWindow: time.Minute,
		LoginLimiterCapacity: 16, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.UserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, stored
}

func TestLoginAuthenticateAndLogout(t *testing.T) {
	now := time.Now()
	service, _, _ := newTestService(t, func() time.Time { return now })
	ctx := context.Background()

	result, err := service.Login(ctx, "alice", "secret-password", "127.0.0.1")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Token == "" || result.User.Username != "alice" {
		t.Fatalf("result = %+v", result)
	}

	identity, err := service.Authenticate(ctx, result.Token)
	if err != nil || identity.UserID != "u1" {
		t.Fatalf("Authenticate = %+v, %v", identity, err)
	}

	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatalf("重复 Logout: %v", err)
	}
	if _, err := service.Authenticate(ctx, result.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("撤销后认证错误 = %v", err)
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	now := time.Now()
	service, _, _ := newTestService(t, func() time.Time { return now })
	ctx := context.Background()

	if _, err := service.Login(ctx, "alice", "wrong-password", "ip1"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("错误密码 = %v", err)
	}
	if _, err := service.Login(ctx, "missing", "wrong-password", "ip2"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("未知账号 = %v", err)
	}
}

func TestLoginRateLimitIsBoundedAndResets(t *testing.T) {
	now := time.Now()
	service, _, _ := newTestService(t, func() time.Time { return now })
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, _ = service.Login(ctx, "alice", "wrong-password", "ip")
	}
	if _, err := service.Login(ctx, "alice", "wrong-password", "ip"); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("超过限制错误 = %v", err)
	}
	// 不同客户端不受影响。
	if _, err := service.Login(ctx, "alice", "secret-password", "other-ip"); err != nil {
		t.Fatalf("其他客户端登录: %v", err)
	}
	// 窗口过期后恢复。
	now = now.Add(2 * time.Minute)
	if _, err := service.Login(ctx, "alice", "secret-password", "ip"); err != nil {
		t.Fatalf("窗口后登录: %v", err)
	}
}

func TestTokenExpiryAndMultipleDevices(t *testing.T) {
	now := time.Now()
	service, store, user := newTestService(t, func() time.Time { return now })
	ctx := context.Background()

	first, err := service.Login(ctx, "alice", "secret-password", "ip-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Login(ctx, "alice", "secret-password", "ip-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token {
		t.Fatal("多设备应产生独立 Token")
	}

	if err := service.Logout(ctx, first.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, second.Token); err != nil {
		t.Fatalf("另一设备 Token 不应被退出影响: %v", err)
	}

	now = now.Add(2 * time.Hour)
	if _, err := service.Authenticate(ctx, second.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("过期 Token 应失效: %v", err)
	}

	user.Enabled = false
	store.sessions[TokenHash("manual")] = &LoginSession{
		TokenHash: TokenHash("manual"), UserID: "u1", ExpiresAt: now.Add(time.Hour),
	}
	if _, err := service.Authenticate(ctx, "manual"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("禁用用户 Token 应失效: %v", err)
	}
}

func TestDisabledUserCannotLogin(t *testing.T) {
	now := time.Now()
	service, _, user := newTestService(t, func() time.Time { return now })
	user.Enabled = false

	if _, err := service.Login(context.Background(), "alice", "secret-password", "ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("禁用用户登录错误 = %v", err)
	}
}
