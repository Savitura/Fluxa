package tenant

import "context"

type contextKey struct{}
type userIDKey struct{}
type roleKey struct{}

// WithID attaches a tenant ID to the context.
func WithID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, contextKey{}, tenantID)
}

// IDFromContext returns the tenant ID from context, or empty if unset.
func IDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}

// WithUser attaches a user ID and role to context.
func WithUser(ctx context.Context, userID, role string) context.Context {
	ctx = context.WithValue(ctx, userIDKey{}, userID)
	return context.WithValue(ctx, roleKey{}, role)
}

// UserIDFromContext returns the user ID from context, or empty if unset.
func UserIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(userIDKey{}).(string)
	return id
}

// RoleFromContext returns the user role from context, or empty if unset.
func RoleFromContext(ctx context.Context) string {
	role, _ := ctx.Value(roleKey{}).(string)
	return role
}

