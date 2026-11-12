package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"gooncall-agent/internal/auth"
	"gooncall-agent/internal/session"
)

// streamEvents 订阅运行事件。客户端断连只退订，不影响后台执行。
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, identity *auth.Identity) {
	runID := r.PathValue("id")

	// 先校验归属，再订阅；随后重新读取快照，避免漏掉订阅前已发生的事件。
	if _, err := s.svc.RunDetail(r.Context(), identity.UserID, runID); err != nil {
		s.writeServiceError(w, err)
		return
	}
	events, unsubscribe := s.svc.Subscribe(runID)
	defer unsubscribe()

	detail, err := s.svc.RunDetail(r.Context(), identity.UserID, runID)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前连接不支持流式输出")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	snapshot := struct {
		Type      string         `json:"type"`
		Run       *session.Run   `json:"run"`
		Approvals []approvalView `json:"pending_approvals"`
	}{
		Type:      "snapshot",
		Run:       detail.Run,
		Approvals: approvalViews(detail),
	}
	if !writeSSE(w, "snapshot", snapshot) {
		return
	}
	flusher.Flush()

	if detail.Run.Status.Terminal() {
		return
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if !writeSSE(w, event.Type, event) {
				return
			}
			flusher.Flush()
			if event.Type == "run_finished" {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, event string, value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return false
	}
	return true
}
