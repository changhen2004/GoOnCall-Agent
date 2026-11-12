// Package auth 提供本地用户密码验证与随机 Token 登录会话。
package auth

import (
	"errors"
	"time"
)

var (
	// ErrInvalidCredentials 表示用户名、密码或账号状态无效；登录响应不区分具体原因。
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	// ErrUnauthenticated 表示 Token 缺失、过期、已撤销或用户已禁用。
	ErrUnauthenticated = errors.New("未认证或登录已失效")
	// ErrTooManyAttempts 表示登录尝试超过限制。
	ErrTooManyAttempts = errors.New("登录尝试过于频繁")
	// ErrUserDisabled 表示用户已被禁用。
	ErrUserDisabled = errors.New("用户已禁用")
	// ErrUsernameTaken 表示用户名已存在。
	ErrUsernameTaken = errors.New("用户名已存在")
	// ErrNotFound 表示记录不存在。
	ErrNotFound = errors.New("记录不存在")
	// ErrInvalidUser 表示用户名或密码不符合要求。
	ErrInvalidUser = errors.New("用户名或密码不符合要求")
)

// User 是本地账号。PasswordHash 永不写入日志或接口响应。
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// LoginSession 是服务端登录会话；数据库只保存 Token 摘要。
type LoginSession struct {
	TokenHash string     `json:"-"`
	UserID    string     `json:"user_id"`
	ExpiresAt time.Time  `json:"expires_at"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// LoginResult 是登录成功的返回值；Token 只在此处返回一次。
type LoginResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
	User      User      `json:"user"`
}

// Identity 是受保护接口使用的最小身份信息。
type Identity struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}
