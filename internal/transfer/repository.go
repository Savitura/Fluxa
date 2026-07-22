package transfer

import (
	"context"

	"github.com/fluxa/fluxa/internal/domain"
)

type Repository interface {
	Create(ctx context.Context, tx *domain.Transaction) error
	GetByID(ctx context.Context, id string) (*domain.Transaction, error)
	UpdateStatus(ctx context.Context, id string, status domain.TransactionStatus, txHash string) error
	ListByWallet(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error)
	ListByBatch(ctx context.Context, batchID string) ([]*domain.Transaction, error)
	CountMonthlyTransfersByTenant(ctx context.Context, tenantID string, year int, month time.Month) (int, error)
}
