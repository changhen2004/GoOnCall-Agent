package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const (
	webhookToken = "webhook-token-abc"
	readToken    = "read-token-def"
	adminToken   = "admin-token-ghi"
	adminActor   = "alice@ops"
)

func testTokens() AuthConfig {
	return AuthConfig{
		WebhookToken: webhookToken,
		ReadToken:    readToken,
		AdminToken:   adminToken,
		AdminActor:   adminActor,
	}
}

// protectedRouter 用给定中间件保护 /protected，并回显上下文里的 actor。
func protectedRouter(mw gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", mw, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"actor": Actor(c)})
	})
	return r
}

func doAuth(t *testing.T, r *gin.Engine, header, value string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	if header != "" {
		req.Header.Set(header, value)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestReadAuth(t *testing.T) {
	r := protectedRouter(ReadAuth(testTokens()))

	if w := doAuth(t, r, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", w.Code)
	}
	if w := doAuth(t, r, "Authorization", "Bearer wrong-token"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", w.Code)
	}
	if w := doAuth(t, r, "Authorization", webhookToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("webhook token on read route status = %d, want 401", w.Code)
	}
	if w := doAuth(t, r, "Authorization", "Bearer "+readToken); w.Code != http.StatusOK {
		t.Fatalf("read token status = %d, want 200", w.Code)
	}
	// admin 同时具备读权限，并以 admin actor 记录操作者。
	w := doAuth(t, r, "Authorization", "Bearer "+adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("admin token on read route status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, adminActor) {
		t.Fatalf("admin actor missing from context: %s", body)
	}
}

func TestAdminAuth(t *testing.T) {
	r := protectedRouter(AdminAuth(testTokens()))

	if w := doAuth(t, r, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token status = %d, want 401", w.Code)
	}
	if w := doAuth(t, r, "Authorization", "Bearer nope"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", w.Code)
	}
	// 已认证但权限不足：403，而不是 401。
	if w := doAuth(t, r, "Authorization", "Bearer "+readToken); w.Code != http.StatusForbidden {
		t.Fatalf("read token on admin route status = %d, want 403", w.Code)
	}
	w := doAuth(t, r, "Authorization", "Bearer "+adminToken)
	if w.Code != http.StatusOK {
		t.Fatalf("admin token status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, adminActor) {
		t.Fatalf("admin actor missing from context: %s", body)
	}
}

func TestWebhookAuth(t *testing.T) {
	r := protectedRouter(WebhookAuth(testTokens()))

	if w := doAuth(t, r, "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no header status = %d, want 401", w.Code)
	}
	// Authorization 头不能替代 webhook 头。
	if w := doAuth(t, r, "Authorization", "Bearer "+adminToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("bearer on webhook route status = %d, want 401", w.Code)
	}
	if w := doAuth(t, r, "X-GoOnCall-Webhook-Token", "bad"); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad webhook token status = %d, want 401", w.Code)
	}
	if w := doAuth(t, r, "X-GoOnCall-Webhook-Token", webhookToken); w.Code != http.StatusOK {
		t.Fatalf("webhook token status = %d, want 200", w.Code)
	}
}

// TestTokenMatches_FailClosed 配置为空或未提供 Token 时一律不通过。
func TestTokenMatches_FailClosed(t *testing.T) {
	if tokenMatches("", "anything") {
		t.Fatal("empty configured token must never match")
	}
	if tokenMatches("configured", "") {
		t.Fatal("empty presented token must never match")
	}
	if !tokenMatches("configured", "configured") {
		t.Fatal("equal tokens must match")
	}
}

// TestAuth_ResponseDoesNotEchoToken 认证失败响应不能回显请求里的 Token。
func TestAuth_ResponseDoesNotEchoToken(t *testing.T) {
	r := protectedRouter(AdminAuth(testTokens()))
	w := doAuth(t, r, "Authorization", "Bearer attacker-supplied-secret")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if strings.Contains(w.Body.String(), "attacker-supplied-secret") {
		t.Fatalf("response echoes the presented token: %s", w.Body.String())
	}
}
