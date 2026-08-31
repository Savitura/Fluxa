package wallet

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/stellar/go/clients/horizonclient"
)

type Service struct {
	repo           *Repository
	horizonClient  *horizonclient.Client
	cacheTTL       time.Duration
	lagThreshold   time.Duration
}

func NewService(repo *Repository, horizonClient *horizonclient.Client) *Service {
	return &Service{
		repo:          repo,
		horizonClient: horizonClient,
		cacheTTL:      2 * time.Minute,
		lagThreshold:  5 * time.Minute,
	}
}

func (s *Service) GetWalletBalances(ctx context.Context, tenantID, walletID string) (*domain.WalletBalance, error) {
	wallet, err := s.repo.GetByID(ctx, tenantID, walletID)
	if err != nil {
		return nil, err
	}

	// Check Horizon server ledger/sync status or lag if client is available
	stale := false
	if s.horizonClient != nil {
		root, err := s.horizonClient.Root()
		if err == nil {
			// If Horizon exposes historyLatestLedgerClosedAt or similar, or we check ledger timestamps
			if !root.HistoryLatestLedgerClosedAt.IsZero() {
				if time.Since(root.HistoryLatestLedgerClosedAt) > s.lagThreshold {
					stale = true
				}
			}
		}
	}

	balances, updatedAt, err := s.repo.GetCachedBalances(ctx, walletID)
	if err != nil || stale || time.Since(updatedAt) > s.cacheTTL {
		// Force fresh fetch from Stellar Horizon if stale or cache expired
		bal, fetchErr := s.fetchFreshBalances(ctx, wallet.PublicKey)
		if fetchErr == nil {
			balances = bal
			_ = s.repo.CacheBalances(ctx, walletID, balances)
		}
	}

	return &domain.WalletBalance{
		WalletID:  walletID,
		Balances:  balances,
		Stale:     stale,
		UpdatedAt: time.Now(),
	}, nil
}

func (s *Service) fetchFreshBalances(ctx context.Context, publicKey string) ([]domain.Balance, error) {
	if s.horizonClient == nil {
		return []domain.Balance{},
	}
	account, err := s.horizonClient.AccountDetail(horizonclient.AccountRequest{AccountID: publicKey})
	if err != nil {
		return nil, err
	}
	var balances []domain.Balance
	for _, b := range account.Balances {
		balances = append(balances, domain.Balance{
			AssetCode:   b.Asset.Code,
			AssetIssuer: b.Asset.Issuer,
			Balance:     b.Balance,
			Limit:       b.Limit,
		})
	}
	return balances, nil
}
