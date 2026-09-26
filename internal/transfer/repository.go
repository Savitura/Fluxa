package transfer

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

type Repository interface {
	Create(ctx context.Context, tx *domain.Transaction) error
	// CreateWithMonthlyLimit atomically checks the tenant's monthly transfer
	// count and inserts the transaction in a single database transaction,
	// preventing concurrent requests from exceeding the quota.
	CreateWithMonthlyLimit(ctx context.Context, tx *domain.Transaction, tenantID string, year int, month time.Month, limit int) error
	GetByID(ctx context.Context, id string) (*domain.Transaction, error)
	// ClaimForSubmission atomically transitions a transaction from pending
	// to submitted. Only the caller that wins this transition may build/
	// sign/submit a Stellar transaction for this id — it returns
	// domain.ErrConcurrentUpdate when another worker already holds the
	// claim or the row is not pending (already submitted, or in a terminal
	// state). A stuck submitted transaction that never got a hash recorded
	// (a prior attempt crashed before reaching the network) is not
	// reclaimed here — see the reconciliation package's stuck-transaction
	// recovery, which resets it back to pending (gated on age) before
	// re-enqueueing.
	ClaimForSubmission(ctx context.Context, id string) error
	UpdateStatus(ctx context.Context, id string, status domain.TransactionStatus, txHash string) error
	ListByWallet(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error)
	UpsertByTxHash(ctx context.Context, tx *domain.Transaction) error
	ListByBatch(ctx context.Context, batchID string) ([]*domain.Transaction, error)
	CountMonthlyTransfersByTenant(ctx context.Context, tenantID string, year int, month time.Month) (int, error)
	// ExistsByTxHash reports whether a transaction with the given Stellar hash
	// has already been recorded, used to keep indexer sync idempotent.
	ExistsByTxHash(ctx context.Context, txHash string) (bool, error)
	// GetByIdempotencyKey returns the transaction previously created for this
	// org/idempotency-key pair, or domain.ErrTransactionNotFound if none exists.
	GetByIdempotencyKey(ctx context.Context, orgID, idempotencyKey string) (*domain.Transaction, error)
}
