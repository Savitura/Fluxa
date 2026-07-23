package wallet

import (
	"context"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

type Repository interface {
	Create(ctx context.Context, w *domain.Wallet) error
	GetByID(ctx context.Context, id string) (*domain.Wallet, error)
	GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error)
	List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error)
	// UpsertBalance persists the current on-chain balance for a wallet/asset pair.
	UpsertBalance(ctx context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error
	// UpdateSyncCursor advances the Horizon paging token used to resume incremental sync.
	UpdateSyncCursor(ctx context.Context, walletID, cursor string) error
}
