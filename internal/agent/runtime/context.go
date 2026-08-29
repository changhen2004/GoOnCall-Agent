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
