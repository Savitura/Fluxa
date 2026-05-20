package indexer

import (
	"context"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	horizonclient "github.com/stellar/go/clients/horizonclient"
	"time"
)

type Indexer struct {
	walletRepo wallet.Repository
	txRepo     transfer.Repository
	stellar    stellar.Client
}

func New(walletRepo wallet.Repository, txRepo transfer.Repository, stellarClient stellar.Client) *Indexer {
	return &Indexer{
		walletRepo: walletRepo,
		txRepo:     txRepo,
		stellar:    stellarClient,
	}
}

// SyncAll iterates over all wallets and syncs their recent payments from Horizon.
func (idx *Indexer) SyncAll(ctx context.Context, limit, offset int) error {
	wallets, err := idx.walletRepo.List(ctx, limit, offset)
	if err != nil {
		return fmt.Errorf("list wallets: %w", err)
	}

	for _, w := range wallets {
		if err := idx.SyncWallet(ctx, w); err != nil {
			log.Error().Err(err).Str("wallet_id", w.ID).Msg("failed to sync wallet")
		}
	}
	return nil
}

// SyncWallet syncs Horizon payment history for a single wallet into the local DB.
func (idx *Indexer) SyncWallet(ctx context.Context, w *domain.Wallet) error {
	acct, err := idx.stellar.LoadAccount(w.PublicKey)
	if err != nil {
		hErr, ok := err.(*horizonclient.Error)
		if ok && hErr.Response.Status == 404 {
			return nil // account not yet funded — nothing to sync
		}
		return fmt.Errorf("load account: %w", err)
	}

	_ = acct // future: use cursor from local state

	return nil
}

func newInboundTransaction(walletID, publicKey, txHash, asset, amount string) (*domain.Transaction, error) {
	amt, err := decimal.NewFromString(amount)
	if err != nil {
		return nil, err
	}
	return &domain.Transaction{
		ID:        uuid.New().String(),
		TxHash:    txHash,
		Type:      domain.TypeTransfer,
		Status:    domain.StatusConfirmed,
		ToWallet:  walletID,
		Asset:     asset,
		Amount:    amt,
		CreatedAt: time.Now().UTC(),
	}, nil
}
