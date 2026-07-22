package wallet

import (
	"context"

	"github.com/fluxa/fluxa/internal/domain"
)

type Repository interface {
	Create(ctx context.Context, w *domain.Wallet) error
	GetByID(ctx context.Context, id string) (*domain.Wallet, error)
	GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error)
	List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error)
	CountByTenant(ctx context.Context, tenantID string) (int, error)
}
