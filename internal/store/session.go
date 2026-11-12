package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gooncall-agent/internal/llm"
	"gooncall-agent/internal/session"
)

// CreateConversation 保存会话。
func (d *DB) CreateConversation(ctx context.Context, conversation session.Conversation) error {
	kind := conversation.OwnerKind
	if kind == "" {
		kind = session.OwnerKindUser
	}
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO conversations (id, owner, owner_kind, created_at) VALUES (?, ?, ?, ?)`,
		conversation.ID, conversation.Owner, kind, millis(conversation.CreatedAt))
	if err != nil {
		return fmt.Errorf("创建会话: %w", err)
	}
	return nil
}

// Conversation 查询会话。
func (d *DB) Conversation(ctx context.Context, id string) (*session.Conversation, error) {
	var conversation session.Conversation
	var created int64
	err := d.db.QueryRowContext(ctx,
		`SELECT id, owner, owner_kind, created_at FROM conversations WHERE id = ?`, id).
		Scan(&conversation.ID, &conversation.Owner, &conversation.OwnerKind, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询会话: %w", err)
	}
	conversation.CreatedAt = fromMillis(created)
	return &conversation, nil
}

// StartRun 在事务中创建运行并保存用户消息。
func (d *DB) StartRun(ctx context.Context, run session.Run, userMessage session.Message) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO runs (id, conversation_id, model, status, budget_ms, reason, process_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ConversationID, run.Model, string(run.Status), run.BudgetMS, run.Reason,
		run.ProcessID, millis(run.CreatedAt), millis(run.UpdatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return session.ErrRunActive
		}
		return fmt.Errorf("创建运行: %w", err)
	}

	if err := insertMessage(ctx, tx, userMessage); err != nil {
		return err
	}
	return tx.Commit()
}

// Run 查询单次运行。
func (d *DB) Run(ctx context.Context, id string) (*session.Run, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, conversation_id, model, status, budget_ms, reason, process_id, created_at, updated_at
		 FROM runs WHERE id = ?`, id)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询运行: %w", err)
	}
	return run, nil
}

// Runs 查询会话的全部运行。
func (d *DB) Runs(ctx context.Context, conversationID string) ([]session.Run, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, conversation_id, model, status, budget_ms, reason, process_id, created_at, updated_at
		 FROM runs WHERE conversation_id = ? ORDER BY created_at`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("查询运行列表: %w", err)
	}
	defer rows.Close()

	var out []session.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// ActiveRunsByOwner 查询指定新用户的活动运行。
func (d *DB) ActiveRunsByOwner(ctx context.Context, ownerID string) ([]session.Run, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT r.id, r.conversation_id, r.model, r.status, r.budget_ms, r.reason,
		        r.process_id, r.created_at, r.updated_at
		 FROM runs r
		 JOIN conversations c ON c.id = r.conversation_id
		 WHERE c.owner = ? AND c.owner_kind = 'user'
		   AND r.status IN ('running', 'waiting_approval')
		 ORDER BY r.created_at`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("查询用户活动运行: %w", err)
	}
	defer rows.Close()

	var out []session.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// Messages 查询会话的全部消息（含草稿）。
func (d *DB) Messages(ctx context.Context, conversationID string) ([]session.Message, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, conversation_id, run_id, seq, role, content, tool_calls, tool_call_id, name, status, created_at
		 FROM messages WHERE conversation_id = ? ORDER BY seq`, conversationID)
	if err != nil {
		return nil, fmt.Errorf("查询消息: %w", err)
	}
	defer rows.Close()

	var out []session.Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *message)
	}
	return out, rows.Err()
}

// CompletedHistory 查询可进入模型上下文的完整历史。
func (d *DB) CompletedHistory(ctx context.Context, conversationID, excludeRunID string) ([]session.Message, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT m.id, m.conversation_id, m.run_id, m.seq, m.role, m.content,
		        m.tool_calls, m.tool_call_id, m.name, m.status, m.created_at
		 FROM messages m
		 JOIN runs r ON r.id = m.run_id
		 WHERE m.conversation_id = ? AND m.run_id <> ?
		   AND r.status = 'completed' AND m.status = 'complete'
		 ORDER BY r.created_at, m.seq`, conversationID, excludeRunID)
	if err != nil {
		return nil, fmt.Errorf("查询历史消息: %w", err)
	}
	defer rows.Close()

	var out []session.Message
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *message)
	}
	return out, rows.Err()
}

// AppendMessage 追加一条消息，序号在 SQL 内递增。
func (d *DB) AppendMessage(ctx context.Context, message session.Message) error {
	return insertMessage(ctx, d.db, message)
}

// UpdateRunStatus 条件更新运行状态。
func (d *DB) UpdateRunStatus(ctx context.Context, runID string, to session.RunStatus, reason string, from ...session.RunStatus) (bool, error) {
	query := `UPDATE runs SET status = ?, reason = ?, updated_at = ? WHERE id = ?`
	args := []any{string(to), reason, time.Now().UnixMilli(), runID}
	if len(from) > 0 {
		placeholders := make([]string, len(from))
		for i, status := range from {
			placeholders[i] = "?"
			args = append(args, string(status))
		}
		query += ` AND status IN (` + strings.Join(placeholders, ",") + `)`
	}

	result, err := d.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("更新运行状态: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// CreateToolExecution 保存开始执行的工具。
func (d *DB) CreateToolExecution(ctx context.Context, execution session.ToolExecution) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO tool_executions
		 (id, run_id, tool_call_id, tool_name, arguments, requires_approval, status, result, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		execution.ID, execution.RunID, execution.ToolCallID, execution.ToolName,
		execution.Arguments, boolInt(execution.RequiresApproval), string(execution.Status),
		execution.Result, millis(execution.CreatedAt), millis(execution.UpdatedAt))
	if err != nil {
		return fmt.Errorf("创建工具执行记录: %w", err)
	}
	return nil
}

// UpdateToolExecution 更新工具执行状态和结果。
func (d *DB) UpdateToolExecution(ctx context.Context, id string, status session.ToolExecutionStatus, result string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE tool_executions SET status = ?, result = ?, updated_at = ? WHERE id = ?`,
		string(status), result, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("更新工具执行记录: %w", err)
	}
	return nil
}

// FinishToolExecution 在同一事务中更新工具执行结果并保存对应工具消息。
func (d *DB) FinishToolExecution(ctx context.Context, id string, status session.ToolExecutionStatus, result string, message session.Message) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE tool_executions SET status = ?, result = ?, updated_at = ? WHERE id = ?`,
		string(status), result, time.Now().UnixMilli(), id); err != nil {
		return fmt.Errorf("更新工具执行记录: %w", err)
	}
	if err := insertMessage(ctx, tx, message); err != nil {
		return err
	}
	return tx.Commit()
}

// BeginApproval 创建工具执行、审批记录并暂停运行。
func (d *DB) BeginApproval(ctx context.Context, execution session.ToolExecution, approval session.Approval) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`UPDATE runs SET status = 'waiting_approval', updated_at = ? WHERE id = ? AND status = 'running'`,
		time.Now().UnixMilli(), execution.RunID)
	if err != nil {
		return false, fmt.Errorf("暂停运行: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO tool_executions
		 (id, run_id, tool_call_id, tool_name, arguments, requires_approval, status, result, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 1, ?, '', ?, ?)`,
		execution.ID, execution.RunID, execution.ToolCallID, execution.ToolName,
		execution.Arguments, string(execution.Status),
		millis(execution.CreatedAt), millis(execution.UpdatedAt)); err != nil {
		return false, fmt.Errorf("创建待审批工具执行: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO approvals (id, tool_execution_id, run_id, status, operator, expires_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, '', ?, ?, ?)`,
		approval.ID, approval.ToolExecutionID, approval.RunID, string(approval.Status),
		approval.ExpiresAt.UnixMilli(), millis(approval.CreatedAt), millis(approval.UpdatedAt)); err != nil {
		return false, fmt.Errorf("创建审批记录: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ClaimApproval 原子取得工具执行权并恢复运行。
func (d *DB) ClaimApproval(ctx context.Context, approvalID string, now time.Time) (bool, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var executionID, runID string
	err = tx.QueryRowContext(ctx,
		`SELECT a.tool_execution_id, a.run_id
		 FROM approvals a
		 JOIN tool_executions te ON te.id = a.tool_execution_id
		 WHERE a.id = ? AND a.status = 'approved' AND a.expires_at > ?
		   AND te.status = 'pending_approval'`,
		approvalID, now.UnixMilli()).Scan(&executionID, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("检查审批: %w", err)
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE runs SET status = 'running', updated_at = ? WHERE id = ? AND status = 'waiting_approval'`,
		now.UnixMilli(), runID)
	if err != nil {
		return false, fmt.Errorf("恢复运行: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}

	result, err = tx.ExecContext(ctx,
		`UPDATE tool_executions SET status = 'running', updated_at = ? WHERE id = ? AND status = 'pending_approval'`,
		now.UnixMilli(), executionID)
	if err != nil {
		return false, fmt.Errorf("取得执行权: %w", err)
	}
	n, err = result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}

	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ExpireApproval 把待审批记录置为过期。
func (d *DB) ExpireApproval(ctx context.Context, approvalID string, now time.Time) (bool, error) {
	result, err := d.db.ExecContext(ctx,
		`UPDATE approvals SET status = 'expired', updated_at = ? WHERE id = ? AND status = 'pending'`,
		now.UnixMilli(), approvalID)
	if err != nil {
		return false, fmt.Errorf("更新审批状态: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// DecideApproval 原子记录审批决定；过期或重复决定返回 false。
func (d *DB) DecideApproval(ctx context.Context, approvalID string, status session.ApprovalStatus, operator string, now time.Time) (bool, error) {
	result, err := d.db.ExecContext(ctx,
		`UPDATE approvals SET status = ?, operator = ?, updated_at = ?
		 WHERE id = ? AND status = 'pending' AND expires_at > ?`,
		string(status), operator, now.UnixMilli(), approvalID, now.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("记录审批决定: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// InvalidateApprovals 使运行的待审批项失效。
func (d *DB) InvalidateApprovals(ctx context.Context, runID string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE approvals SET status = 'invalid', updated_at = ?
		 WHERE run_id = ? AND status = 'pending'`,
		time.Now().UnixMilli(), runID)
	if err != nil {
		return fmt.Errorf("失效审批: %w", err)
	}
	return nil
}

// Approval 查询审批。
func (d *DB) Approval(ctx context.Context, id string) (*session.Approval, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, tool_execution_id, run_id, status, operator, expires_at, created_at, updated_at
		 FROM approvals WHERE id = ?`, id)
	approval, err := scanApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询审批: %w", err)
	}
	return approval, nil
}

// PendingApprovals 查询运行的待审批项。
func (d *DB) PendingApprovals(ctx context.Context, runID string) ([]session.Approval, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, tool_execution_id, run_id, status, operator, expires_at, created_at, updated_at
		 FROM approvals WHERE run_id = ? AND status = 'pending' ORDER BY created_at`, runID)
	if err != nil {
		return nil, fmt.Errorf("查询待审批项: %w", err)
	}
	defer rows.Close()

	var out []session.Approval
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *approval)
	}
	return out, rows.Err()
}

// ToolExecutions 查询运行的工具执行记录。
func (d *DB) ToolExecutions(ctx context.Context, runID string) ([]session.ToolExecution, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT id, run_id, tool_call_id, tool_name, arguments, requires_approval, status, result, created_at, updated_at
		 FROM tool_executions WHERE run_id = ? ORDER BY created_at`, runID)
	if err != nil {
		return nil, fmt.Errorf("查询工具执行记录: %w", err)
	}
	defer rows.Close()

	var out []session.ToolExecution
	for rows.Next() {
		execution, err := scanToolExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *execution)
	}
	return out, rows.Err()
}

// InterruptStale 把旧进程遗留的活动运行标记为中断。
func (d *DB) InterruptStale(ctx context.Context, now time.Time) (session.InterruptReport, error) {
	var report session.InterruptReport
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()

	const staleRuns = `SELECT id FROM runs WHERE status IN ('running', 'waiting_approval')`
	nowMS := now.UnixMilli()

	res, err := tx.ExecContext(ctx,
		`UPDATE approvals SET status = 'invalid', updated_at = ?
		 WHERE status = 'pending' AND run_id IN (`+staleRuns+`)`, nowMS)
	if err != nil {
		return report, fmt.Errorf("失效遗留审批: %w", err)
	}
	report.Approvals, _ = res.RowsAffected()

	res, err = tx.ExecContext(ctx,
		`UPDATE tool_executions SET status = 'canceled', updated_at = ?
		 WHERE status = 'pending_approval' AND run_id IN (`+staleRuns+`)`, nowMS)
	if err != nil {
		return report, fmt.Errorf("取消遗留待审批工具: %w", err)
	}
	n, _ := res.RowsAffected()
	report.ToolExecutions += n

	// 已经开始的写操作无法确认结果，标记为 unknown。
	res, err = tx.ExecContext(ctx,
		`UPDATE tool_executions SET status = 'unknown', updated_at = ?
		 WHERE status = 'running' AND requires_approval = 1 AND run_id IN (`+staleRuns+`)`, nowMS)
	if err != nil {
		return report, fmt.Errorf("标记未知结果: %w", err)
	}
	n, _ = res.RowsAffected()
	report.ToolExecutions += n

	res, err = tx.ExecContext(ctx,
		`UPDATE tool_executions SET status = 'canceled', updated_at = ?
		 WHERE status = 'running' AND requires_approval = 0 AND run_id IN (`+staleRuns+`)`, nowMS)
	if err != nil {
		return report, fmt.Errorf("取消遗留工具执行: %w", err)
	}
	n, _ = res.RowsAffected()
	report.ToolExecutions += n

	res, err = tx.ExecContext(ctx,
		`UPDATE runs SET status = 'interrupted', reason = '服务重启，运行已中断', updated_at = ?
		 WHERE status IN ('running', 'waiting_approval')`, nowMS)
	if err != nil {
		return report, fmt.Errorf("中断遗留运行: %w", err)
	}
	report.Runs, _ = res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

// --- helpers ---

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func insertMessage(ctx context.Context, db execer, message session.Message) error {
	status := message.Status
	if status == "" {
		status = session.MessageComplete
	}
	calls := ""
	if len(message.ToolCalls) > 0 {
		data, err := json.Marshal(message.ToolCalls)
		if err != nil {
			return fmt.Errorf("编码工具调用: %w", err)
		}
		calls = string(data)
	}

	_, err := db.ExecContext(ctx,
		`INSERT INTO messages
		 (conversation_id, run_id, seq, role, content, tool_calls, tool_call_id, name, status, created_at)
		 VALUES (?, ?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM messages WHERE conversation_id = ?),
		         ?, ?, ?, ?, ?, ?, ?)`,
		message.ConversationID, message.RunID, message.ConversationID,
		string(message.Role), message.Content, calls, message.ToolCallID,
		message.Name, string(status), millis(message.CreatedAt))
	if err != nil {
		return fmt.Errorf("保存消息: %w", err)
	}
	return nil
}

func scanRun(row rowScanner) (*session.Run, error) {
	var run session.Run
	var status string
	var created, updated int64
	err := row.Scan(&run.ID, &run.ConversationID, &run.Model, &status, &run.BudgetMS,
		&run.Reason, &run.ProcessID, &created, &updated)
	if err != nil {
		return nil, err
	}
	run.Status = session.RunStatus(status)
	run.CreatedAt = fromMillis(created)
	run.UpdatedAt = fromMillis(updated)
	return &run, nil
}

func scanMessage(row rowScanner) (*session.Message, error) {
	var message session.Message
	var role, status, calls string
	var created int64
	err := row.Scan(&message.ID, &message.ConversationID, &message.RunID, &message.Seq,
		&role, &message.Content, &calls, &message.ToolCallID, &message.Name, &status, &created)
	if err != nil {
		return nil, err
	}
	message.Role = llmRole(role)
	message.Status = session.MessageStatus(status)
	message.CreatedAt = fromMillis(created)
	if calls != "" {
		if err := json.Unmarshal([]byte(calls), &message.ToolCalls); err != nil {
			return nil, fmt.Errorf("解析工具调用: %w", err)
		}
	}
	return &message, nil
}

func scanToolExecution(row rowScanner) (*session.ToolExecution, error) {
	var execution session.ToolExecution
	var requiresApproval int
	var status string
	var created, updated int64
	err := row.Scan(&execution.ID, &execution.RunID, &execution.ToolCallID, &execution.ToolName,
		&execution.Arguments, &requiresApproval, &status, &execution.Result, &created, &updated)
	if err != nil {
		return nil, err
	}
	execution.RequiresApproval = requiresApproval != 0
	execution.Status = session.ToolExecutionStatus(status)
	execution.CreatedAt = fromMillis(created)
	execution.UpdatedAt = fromMillis(updated)
	return &execution, nil
}

func scanApproval(row rowScanner) (*session.Approval, error) {
	var approval session.Approval
	var status string
	var expires, created, updated int64
	err := row.Scan(&approval.ID, &approval.ToolExecutionID, &approval.RunID, &status,
		&approval.Operator, &expires, &created, &updated)
	if err != nil {
		return nil, err
	}
	approval.Status = session.ApprovalStatus(status)
	approval.ExpiresAt = fromMillis(expires)
	approval.CreatedAt = fromMillis(created)
	approval.UpdatedAt = fromMillis(updated)
	return &approval, nil
}

func llmRole(role string) llm.Role { return llm.Role(role) }

func millis(t time.Time) int64 {
	if t.IsZero() {
		return time.Now().UnixMilli()
	}
	return t.UnixMilli()
}

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms) }

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
