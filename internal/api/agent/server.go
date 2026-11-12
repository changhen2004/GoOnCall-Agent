// Package agent 提供通用 Agent 服务的 HTTP/SSE 入口。
package agent

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"gooncall-agent/internal/auth"
	"gooncall-agent/internal/session"
)

// Server 持有会话服务与认证服务。
type Server struct {
	svc    *session.Service
	auth   *auth.Service
	logger *slog.Logger
}

type authedHandler func(w http.ResponseWriter, r *http.Request, identity *auth.Identity)

// New 创建路由；登录与健康检查之外的接口统一使用会话 Token 认证。
func New(svc *session.Service, authSvc *auth.Service, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{svc: svc, auth: authSvc, logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /v1/auth/login", s.login)
	mux.HandleFunc("POST /v1/auth/logout", s.logout)
	mux.HandleFunc("GET /v1/me", s.authenticated(s.me))
	mux.HandleFunc("POST /v1/conversations", s.authenticated(s.createConversation))
	mux.HandleFunc("GET /v1/conversations/{id}", s.authenticated(s.getConversation))
	mux.HandleFunc("POST /v1/conversations/{id}/messages", s.authenticated(s.submitMessage))
	mux.HandleFunc("GET /v1/runs/{id}", s.authenticated(s.getRun))
	mux.HandleFunc("POST /v1/runs/{id}/cancel", s.authenticated(s.cancelRun))
	mux.HandleFunc("GET /v1/runs/{id}/events", s.authenticated(s.streamEvents))
	mux.HandleFunc("GET /v1/runs/{id}/approvals", s.authenticated(s.listApprovals))
	mux.HandleFunc("POST /v1/approvals/{id}/decision", s.authenticated(s.decideApproval))
	return mux
}

func (s *Server) authenticated(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, err := s.auth.Authenticate(r.Context(), bearerToken(r))
		if err != nil {
			if !errors.Is(err, auth.ErrUnauthenticated) {
				s.logger.Error("认证检查失败", "error", err)
			}
			writeError(w, http.StatusUnauthorized, "未认证或登录已失效")
			return
		}
		next(w, r, identity)
	}
}

func bearerToken(r *http.Request) string {
	raw := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(raw) <= len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(raw[len(prefix):])
}

// clientKey 使用连接地址做登录限流键，默认不信任代理头。
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是有效 JSON")
		return false
	}
	return true
}

func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrNotFound):
		writeError(w, http.StatusNotFound, "记录不存在")
	case errors.Is(err, session.ErrForbidden):
		writeError(w, http.StatusForbidden, "无权操作该会话")
	case errors.Is(err, session.ErrRunActive):
		writeError(w, http.StatusConflict, "会话已有运行中的任务")
	case errors.Is(err, session.ErrConflict):
		writeError(w, http.StatusConflict, "状态冲突")
	case errors.Is(err, session.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.logger.Error("请求处理失败", "error", err)
		writeError(w, http.StatusInternalServerError, "内部错误")
	}
}
