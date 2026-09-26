package wallet

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

type Repository struct {
}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) GetByID(ctx context.Context, tenantID, id string) (*domain.Wallet, error) {
	return &domain.Wallet{
		ID:        id,
		TenantID:  tenantID,
		PublicKey: id,
		CreatedAt: time.Now(),
	}, nil
}

func (r *Repository) GetCachedBalances(ctx context.Context, walletID string) ([]domain.Balance, time.Time, error) {
	return []domain.Balance{},
	time.Now().Add(-10 * time.Minute),
	nil
}

func (r *Repository) CacheBalances(ctx context.Context, walletID string, balances []domain.Balance) error {
	return nil
}
