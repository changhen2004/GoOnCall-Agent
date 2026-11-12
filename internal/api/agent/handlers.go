package agent

import (
	"errors"
	"net/http"
	"time"

	"gooncall-agent/internal/auth"
	"gooncall-agent/internal/session"
)

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	result, err := s.auth.Login(r.Context(), body.Username, body.Password, clientKey(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrTooManyAttempts):
			writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		default:
			s.logger.Error("登录失败", "error", err)
			writeError(w, http.StatusInternalServerError, "内部错误")
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// logout 只撤销当前 Token；重复退出保持幂等，不取消后台运行。
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), bearerToken(r)); err != nil {
		s.logger.Error("退出登录失败", "error", err)
		writeError(w, http.StatusInternalServerError, "内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, _ *http.Request, identity *auth.Identity) {
	writeJSON(w, http.StatusOK, identity)
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	conversation, err := s.svc.CreateConversation(r.Context(), identity.UserID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, conversation)
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	conversation, messages, runs, err := s.svc.Conversation(r.Context(), identity.UserID, r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": conversation,
		"messages":     messages,
		"runs":         runs,
	})
}

func (s *Server) submitMessage(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	var body struct {
		Content string `json:"content"`
		Model   string `json:"model"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	run, err := s.svc.Submit(r.Context(), identity.UserID, r.PathValue("id"), body.Content, body.Model)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"run_id": run.ID,
		"status": string(run.Status),
		"model":  run.Model,
	})
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	detail, err := s.svc.RunDetail(r.Context(), identity.UserID, r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"run":               detail.Run,
		"tool_executions":   detail.Tools,
		"pending_approvals": approvalViews(detail),
	})
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	if err := s.svc.Cancel(r.Context(), identity.UserID, r.PathValue("id")); err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(session.RunCanceled)})
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	detail, err := s.svc.RunDetail(r.Context(), identity.UserID, r.PathValue("id"))
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"approvals": approvalViews(detail)})
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	var body struct {
		Decision string `json:"decision"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	approve := false
	switch body.Decision {
	case "approve":
		approve = true
	case "reject":
	default:
		writeError(w, http.StatusBadRequest, `decision 必须是 "approve" 或 "reject"`)
		return
	}

	if err := s.svc.Decide(r.Context(), identity.UserID, r.PathValue("id"), approve); err != nil {
		s.writeServiceError(w, err)
		return
	}
	status := string(session.ApprovalRejected)
	if approve {
		status = string(session.ApprovalApproved)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// approvalView 把审批与对应工具执行的固定参数一起展示；
// 审批只对这条工具执行有效，参数变化或新调用必须重新审批。
type approvalView struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
	ToolCallID string    `json:"tool_call_id"`
	ToolName   string    `json:"tool_name"`
	Arguments  string    `json:"arguments"`
}

func approvalViews(detail *session.RunDetail) []approvalView {
	executions := make(map[string]session.ToolExecution, len(detail.Tools))
	for _, execution := range detail.Tools {
		executions[execution.ID] = execution
	}

	out := make([]approvalView, 0, len(detail.Approvals))
	for _, approval := range detail.Approvals {
		view := approvalView{
			ID:        approval.ID,
			Status:    string(approval.Status),
			ExpiresAt: approval.ExpiresAt,
		}
		if execution, ok := executions[approval.ToolExecutionID]; ok {
			view.ToolCallID = execution.ToolCallID
			view.ToolName = execution.ToolName
			view.Arguments = execution.Arguments
		}
		out = append(out, view)
	}
	return out
}
