package batch

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// ListFilter narrows and orders a batch listing. Zero values mean "no filter".
type ListFilter struct {
	Status       domain.BatchStatus
	FromWallet   string
	AfterCreated time.Time
	AfterID      string
	Limit        int
}

// List returns one page of batches visible to the caller's tenant, newest
// first, plus the keyset cursor for the next page.
type ListResult struct {
	Batches    []*domain.Batch
	NextCursor *ListCursor
}

// ListCursor is the opaque keyset for the next page.
type ListCursor struct {
	CreatedAt time.Time
	ID        string
}

type Repository interface {
	Create(ctx context.Context, b *domain.Batch) error
	GetByID(ctx context.Context, id string) (*domain.Batch, error)
	List(ctx context.Context, filter ListFilter) (*ListResult, error)
}
