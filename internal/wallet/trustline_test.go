package wallet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/base"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

type fullMockRepo struct {
	wallets  map[string]*domain.Wallet
	balances map[string][]domain.BalanceRecord
}

func newFullMockRepo() *fullMockRepo {
	return &fullMockRepo{
		wallets:  make(map[string]*domain.Wallet),
		balances: make(map[string][]domain.BalanceRecord),
	}
}

func (m *fullMockRepo) Create(ctx context.Context, w *domain.Wallet) error {
	m.wallets[w.ID] = w
	return nil
}

func (m *fullMockRepo) GetByID(ctx context.Context, id string) (*domain.Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return w, nil
}

func (m *fullMockRepo) GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error) {
	for _, w := range m.wallets {
		if w.PublicKey == pubKey {
			return w, nil
		}
	}
	return nil, domain.ErrWalletNotFound
}

func (m *fullMockRepo) List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error) {
	var list []*domain.Wallet
	for _, w := range m.wallets {
		list = append(list, w)
	}
	return list, nil
}

func (m *fullMockRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	return len(m.wallets), nil
}

func (m *fullMockRepo) UpsertBalance(ctx context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error {
	recs := m.balances[walletID]
	updated := false
	for i := range recs {
		if recs[i].AssetCode == assetCode && recs[i].Issuer == issuer {
			recs[i].Balance = balance.String()
			updated = true
			break
		}
	}
	if !updated {
		recs = append(recs, domain.BalanceRecord{
			WalletID:  walletID,
			AssetCode: assetCode,
			Issuer:    issuer,
			Balance:   balance.String(),
		})
	}
	m.balances[walletID] = recs
	return nil
}

func (m *fullMockRepo) GetBalances(ctx context.Context, walletID string) ([]domain.BalanceRecord, error) {
	return m.balances[walletID], nil
}

func (m *fullMockRepo) UpdateSyncCursor(ctx context.Context, walletID, cursor string) error {
	if w, ok := m.wallets[walletID]; ok {
		w.SyncCursor = cursor
	}
	return nil
}

type fullMockStellar struct {
	account horizon.Account
	err     error
}

func (m *fullMockStellar) LoadAccount(accountID string) (horizon.Account, error) {
	if m.err != nil {
		return horizon.Account{}, m.err
	}
	return m.account, nil
}
func (m *fullMockStellar) SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{Hash: "mock_tx_hash_123"}, nil
}
func (m *fullMockStellar) FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error) {
	return nil, nil
}
func (m *fullMockStellar) TransactionDetail(hash string) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *fullMockStellar) OperationsForTransaction(hash string) ([]operations.Operation, error) {
	return nil, nil
}
func (m *fullMockStellar) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}

func (m *fullMockStellar) Payments(accountID, cursor string, limit uint) ([]operations.Operation, error) {
	return nil, nil
}
func (m *fullMockStellar) StreamPayments(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error {
	return nil
}
func (m *fullMockStellar) Offers(accountID string, limit uint) ([]horizon.Offer, error) {
	return nil, nil
}

type mockFXService struct{}

func (m *mockFXService) GetRates(ctx context.Context, from, to string) (*fx.RateResponse, error) {
	if from == "XLM" && to == "USD" {
		return &fx.RateResponse{Rate: decimal.NewFromFloat(0.12)}, nil
	}
	if from == "EURC" && to == "USD" {
		return &fx.RateResponse{Rate: decimal.NewFromFloat(1.08)}, nil
	}
	return &fx.RateResponse{Rate: decimal.NewFromFloat(1.0)}, nil
}

func TestGetBalancesMultiAssetWithFX(t *testing.T) {
	repo := newFullMockRepo()
	repo.wallets["w-1"] = &domain.Wallet{ID: "w-1", PublicKey: "GBXLM123"}

	mockStellarClient := &fullMockStellar{
		account: horizon.Account{
			Balances: []horizon.Balance{
				{Asset: base.Asset{Code: ""}, Balance: "100.5000000"},
				{Asset: base.Asset{Code: "USDC", Issuer: "GUSDC123"}, Balance: "50.0000000"},
			},
		},
	}

	masterKey := make([]byte, 32)
	svc := wallet.NewService(repo, mockStellarClient, masterKey).
		WithFXService(&mockFXService{}).
		WithIssuers("GUSDC123", "GEURC123")

	balances, err := svc.GetBalances(context.Background(), "w-1", "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(balances) != 2 {
		t.Fatalf("expected 2 balances, got %d", len(balances))
	}

	// XLM balance: 100.5 * 0.12 = 12.06 USD
	if balances[0].AssetCode != "XLM" || balances[0].USDEquivalent != "12.06" {
		t.Errorf("expected XLM balance with 12.06 USD equivalent, got %v", balances[0])
	}

	// USDC balance: 50.0 USD
	if balances[1].AssetCode != "USDC" || balances[1].USDEquivalent != "50.00" {
		t.Errorf("expected USDC balance with 50.00 USD equivalent, got %v", balances[1])
	}
}

func TestGetBalancesFallbackToDBCache(t *testing.T) {
	repo := newFullMockRepo()
	repo.wallets["w-2"] = &domain.Wallet{ID: "w-2", PublicKey: "GBXLM456"}
	_ = repo.UpsertBalance(context.Background(), "w-2", "USDC", "GUSDC123", decimal.NewFromFloat(25.0))

	// Horizon is down / returns non-404 error
	mockStellarClient := &fullMockStellar{
		err: errors.New("horizon service unavailable"),
	}

	masterKey := make([]byte, 32)
	svc := wallet.NewService(repo, mockStellarClient, masterKey)

	balances, err := svc.GetBalances(context.Background(), "w-2")
	if err != nil {
		t.Fatalf("expected fallback to DB cache, got error: %v", err)
	}

	if len(balances) != 1 || balances[0].AssetCode != "USDC" || balances[0].Balance != "25" {
		t.Fatalf("expected cached USDC balance of 25, got %v", balances)
	}
}
