package batch

import (
	"context"

	"github.com/fluxa/fluxa/internal/domain"
)

type Repository interface {
	Create(ctx context.Context, b *domain.BatchTransfer) error
	GetByID(ctx context.Context, id string) (*domain.BatchTransfer, error)
	// SyncAggregates recomputes the batch's aggregate counts and status from
	// its linked transactions under a row lock and persists them. It returns
	// the updated batch and whether this call transitioned it from a
	// non-terminal to a terminal status — the exactly-once signal behind the
	// batch-level webhooks.
	SyncAggregates(ctx context.Context, id string) (*domain.BatchTransfer, bool, error)
}
