package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// 认证上下文键：handler 从这里取已认证操作者，不再信任请求体。
const (
	actorContextKey = "gooncall_actor"
	roleContextKey  = "gooncall_role"
)

// 认证角色。
const (
	// RoleWebhook 是 Alertmanager webhook 角色。
	RoleWebhook = "webhook"
	// RoleRead 是只读角色。
	RoleRead = "read"
	// RoleAdmin 是管理角色（拥有读权限，可批准 / 拒绝）。
	RoleAdmin = "admin"
)

// AuthConfig 是三类最小认证 Token；空字符串表示该类未启用（仅限显式 Demo 配置）。
type AuthConfig struct {
	// WebhookToken 对应 X-GoOnCall-Webhook-Token。
	WebhookToken string
	// ReadToken 对应 Authorization: Bearer <read-token>。
	ReadToken string
	// AdminToken 对应 Authorization: Bearer <admin-token>；admin 同时拥有读权限。
	AdminToken string
	// AdminActor 是批准 / 拒绝落库的操作者。
	AdminActor string
}

// Enabled 判断是否至少配置了一个 Token（全空 = 显式 Demo，不安装认证中间件）。
func (a AuthConfig) Enabled() bool {
	return a.WebhookToken != "" || a.ReadToken != "" || a.AdminToken != ""
}

// Actor 返回已认证操作者（未认证时为空串）。
func Actor(c *gin.Context) string {
	v, _ := c.Get(actorContextKey)
	s, _ := v.(string)
	return s
}

// WebhookAuth 校验 X-GoOnCall-Webhook-Token。
func WebhookAuth(cfg AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !tokenMatches(cfg.WebhookToken, c.GetHeader("X-GoOnCall-Webhook-Token")) {
			unauthorized(c, "invalid or missing webhook token")
			return
		}
		c.Set(roleContextKey, RoleWebhook)
		c.Next()
	}
}

// ReadAuth 允许 read token 或 admin token（admin 具备读权限）。
//
// 已有合法凭证但权限不足（例如用 read token 调 admin 接口）时由 AdminAuth 返回 403。
func ReadAuth(cfg AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		presented := bearerToken(c)
		switch {
		case tokenMatches(cfg.AdminToken, presented):
			setActor(c, cfg, RoleAdmin)
		case tokenMatches(cfg.ReadToken, presented):
			setActor(c, cfg, RoleRead)
		default:
			unauthorized(c, "invalid or missing read token")
			return
		}
		c.Next()
	}
}

// AdminAuth 只接受 admin token，并把服务端配置的 AdminActor 放进上下文。
//
// 携带了合法的 read token 但调用 admin 接口时返回 403（已认证、权限不足），
// 其余情况（缺失或错误 Token）返回 401。
func AdminAuth(cfg AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		presented := bearerToken(c)
		switch {
		case tokenMatches(cfg.AdminToken, presented):
			setActor(c, cfg, RoleAdmin)
		case tokenMatches(cfg.ReadToken, presented):
			forbidden(c, "admin token required")
			return
		default:
			unauthorized(c, "invalid or missing admin token")
			return
		}
		c.Next()
	}
}

func setActor(c *gin.Context, cfg AuthConfig, role string) {
	c.Set(roleContextKey, role)
	if role == RoleAdmin {
		actor := cfg.AdminActor
		if actor == "" {
			actor = "admin"
		}
		c.Set(actorContextKey, actor)
	}
}

// bearerToken 解析 Authorization: Bearer <token>（大小写不敏感的前缀）。
func bearerToken(c *gin.Context) string {
	raw := c.GetHeader("Authorization")
	const prefix = "bearer "
	if len(raw) <= len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(raw[len(prefix):])
}

// tokenMatches 常量时间比较 Token；任一为空一律不通过（fail-closed）。
func tokenMatches(configured, presented string) bool {
	if configured == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(configured), []byte(presented)) == 1
}

// unauthorized 返回 401，响应体不回显 Token。
func unauthorized(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg})
}

// forbidden 返回 403。
func forbidden(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": msg})
}
