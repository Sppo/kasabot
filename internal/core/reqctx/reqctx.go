package reqctx

import "context"

type ctxKey int

const (
	ctxKeyUserID ctxKey = iota + 1
	ctxKeyWorkspaceID
	ctxKeyWorkspaceRole
	ctxKeyRequestID
)

// WithUserID зберігає ID авторизованого користувача в context.Context.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, userID)
}

// UserIDFromCtx повертає ID користувача з контексту запиту.
func UserIDFromCtx(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKeyUserID).(int64)
	return id, ok && id > 0
}

// WithWorkspaceID зберігає ID поточного воркспейсу в context.Context.
func WithWorkspaceID(ctx context.Context, workspaceID int64) context.Context {
	return context.WithValue(ctx, ctxKeyWorkspaceID, workspaceID)
}

// WorkspaceIDFromCtx повертає ID активного воркспейсу з контексту запиту.
func WorkspaceIDFromCtx(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxKeyWorkspaceID).(int64)
	return id, ok && id > 0
}

// WithWorkspaceRole зберігає роль користувача у поточному воркспейсі в context.Context.
func WithWorkspaceRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, ctxKeyWorkspaceRole, role)
}

// WorkspaceRoleFromCtx повертає роль користувача у поточному воркспейсі з контексту запиту.
func WorkspaceRoleFromCtx(ctx context.Context) string {
	role, _ := ctx.Value(ctxKeyWorkspaceRole).(string)
	return role
}

// WithRequestID зберігає ID HTTP-запиту в context.Context.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, requestID)
}

// RequestIDFromCtx повертає ID HTTP-запиту з контексту.
func RequestIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID).(string)
	return id
}
