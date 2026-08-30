package compliance

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// SanctionsReader is the read side of the sanctions list, split out because
// SanctionsSet only ever reads and is shared with processes that never write.
type SanctionsReader interface {
	ListSanctionsEntities(ctx context.Context) ([]*domain.SanctionsEntity, error)
}

// Repository is the storage contract owned by this package, per the repo
// convention that interfaces live with their consumer.
type Repository interface {
	SanctionsReader

	CreateReview(ctx context.Context, r *domain.ComplianceReview) error
	GetReview(ctx context.Context, id string) (*domain.ComplianceReview, error)
	ListReviews(ctx context.Context, status string, limit, offset int) ([]*domain.ComplianceReview, error)
	// DecideReview transitions a review out of pending. It must fail with
	// domain.ErrReviewNotPending if the review has already been decided, so
	// two concurrent approvals cannot both enqueue the same transfer.
	DecideReview(ctx context.Context, id string, status domain.ReviewStatus, reviewedBy *string, notes string, decidedAt time.Time) error

	CreateBlock(ctx context.Context, b *domain.ComplianceBlock) error

	// ReplaceSanctionsEntities upserts the parsed list and returns the number
	// of rows written.
	ReplaceSanctionsEntities(ctx context.Context, entities []*domain.SanctionsEntity, refreshedAt time.Time) (int, error)
	RecordSanctionsUpdate(ctx context.Context, u *domain.SanctionsUpdate) error
	LatestSanctionsUpdate(ctx context.Context) (*domain.SanctionsUpdate, error)
}

// TransactionGate is the narrow view of the transactions table the review
// workflow needs to release or reject a held transfer.
type TransactionGate interface {
	GetByID(ctx context.Context, id string) (*domain.Transaction, error)
	UpdateStatus(ctx context.Context, id string, status domain.TransactionStatus, txHash string) error
}

// Enqueuer releases an approved transfer back onto the settlement queue.
// *queue.Client satisfies it.
type Enqueuer interface {
	EnqueueTransfer(ctx context.Context, txID string) error
}

// Dispatcher emits compliance webhooks. webhook.Service satisfies it.
type Dispatcher interface {
	Dispatch(ctx context.Context, eventType domain.EventType, payload interface{}) error
}
