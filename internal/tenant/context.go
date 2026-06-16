package tenant

import "context"

type contextKey struct{}

// WithID attaches a tenant ID to the context.
func WithID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, contextKey{}, tenantID)
}

// IDFromContext returns the tenant ID from context, or empty if unset.
func IDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
