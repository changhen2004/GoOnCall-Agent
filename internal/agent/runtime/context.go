package runtime

import "context"

type runIDCtxKey struct{}

// WithRunID 将 Run ID 注入上下文（供工具在请求审批时关联 Run）。
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDCtxKey{}, runID)
}

// RunIDFrom 从上下文读取 Run ID。
func RunIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(runIDCtxKey{}).(string)
	return v
}

type toolCallIDCtxKey struct{}

// WithToolCallID 将 ToolCall ID 注入上下文。
//
// ID 由 Recorder 在调用工具**之前**生成并传入，工具（如 worker_restart 创建审批）引用它，
// 保证持久化的 ToolCall.ID 与 Approval.ToolCallID 指向同一条记录。
func WithToolCallID(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, toolCallIDCtxKey{}, toolCallID)
}

// ToolCallIDFrom 从上下文读取 ToolCall ID（不存在时返回空串）。
func ToolCallIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(toolCallIDCtxKey{}).(string)
	return v
}
