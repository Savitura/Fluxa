package anchor

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// Repository persists anchors and the transactions Fluxa initiates against
// them.
type Repository interface {
	CreateAnchor(ctx context.Context, a *domain.Anchor) error
	ListAnchors(ctx context.Context) ([]*domain.Anchor, error)
	GetAnchorByID(ctx context.Context, id string) (*domain.Anchor, error)
	GetAnchorByHomeDomain(ctx context.Context, homeDomain string) (*domain.Anchor, error)

	CreateTransaction(ctx context.Context, t *domain.AnchorTransaction) error
	GetTransactionByID(ctx context.Context, id string, tenantID *string) (*domain.AnchorTransaction, error)
	UpdateTransactionStatus(ctx context.Context, id, status string, completedAt *time.Time, tenantID *string) error
}
