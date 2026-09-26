package claimable

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// Filter narrows a claimable balance listing. A zero-valued field is ignored,
// so the zero Filter means "every balance visible to the caller".
type Filter struct {
	Status domain.ClaimableBalanceStatus
	Asset  string
	// Claimant matches balances that include this Stellar account as one of
	// their claimants.
	Claimant string
	// ExpiresBefore matches balances whose expiry falls strictly before it.
	ExpiresBefore *time.Time
	Limit         int
	Offset        int
}

// Repository persists claimable balances, scoped to the tenant on the context
// where one is present (the API path) and unscoped where it is not (the expiry
// worker, which sweeps every tenant).
type Repository interface {
	Create(ctx context.Context, b *domain.ClaimableBalance) error
	GetByID(ctx context.Context, id string) (*domain.ClaimableBalance, error)
	List(ctx context.Context, f Filter) ([]*domain.ClaimableBalance, error)
	MarkClaimed(ctx context.Context, id, claimedBy string, at time.Time) error
	MarkExpired(ctx context.Context, id string, at time.Time) error
	MarkRevoked(ctx context.Context, id string, at time.Time) error
	// ListExpiredPending returns pending balances whose expires_at has passed,
	// across every tenant, for the background tracker.
	ListExpiredPending(ctx context.Context, now time.Time) ([]*domain.ClaimableBalance, error)
}
