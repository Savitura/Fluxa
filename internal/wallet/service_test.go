package wallet_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/shopspring/decimal"
)

type basicMockWalletRepo struct {
	wallets map[string]*domain.Wallet
}

func (m *basicMockWalletRepo) Create(ctx context.Context, w *domain.Wallet) error {
	m.wallets[w.ID] = w
	return nil
}
func (m *basicMockWalletRepo) GetByID(ctx context.Context, id string) (*domain.Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return w, nil
}
func (m *basicMockWalletRepo) GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error) {
	return nil, nil
}
func (m *basicMockWalletRepo) List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error) {
	return nil, nil
}
func (m *basicMockWalletRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	return 0, nil
}
func (m *basicMockWalletRepo) UpsertBalance(ctx context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error {
	return nil
}
func (m *basicMockWalletRepo) GetBalances(ctx context.Context, walletID string) ([]domain.BalanceRecord, error) {
	return nil, nil
}
func (m *basicMockWalletRepo) UpdateSyncCursor(ctx context.Context, walletID, cursor string) error {
	return nil
}

func TestCreateWallet_Success(t *testing.T) {
	repo := &basicMockWalletRepo{wallets: make(map[string]*domain.Wallet)}
	masterKey := make([]byte, 32)
	svc := wallet.NewService(repo, nil, masterKey)

	w, err := svc.CreateWallet(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w == nil {
		t.Fatal("expected wallet, got nil")
	}
	if w.ID == "" {
		t.Error("expected wallet ID to be set")
	}
	if w.PublicKey == "" {
		t.Error("expected public key to be set")
	}
	if w.EncryptedSecret == "" {
		t.Error("expected encrypted secret to be set")
	}
	if w.CustodyType != domain.CustodyCustodial {
		t.Errorf("expected custodial wallet, got %s", w.CustodyType)
	}

	// Verify persistence
	if repo.wallets[w.ID] == nil {
		t.Error("expected wallet to be persisted")
	}
}

func TestGetWalletForHandler_Success(t *testing.T) {
	repo := &basicMockWalletRepo{
		wallets: map[string]*domain.Wallet{
			"w1": {ID: "w1", PublicKey: "G1"},
		},
	}
	svc := wallet.NewService(repo, nil, nil)

	w, err := svc.GetWalletForHandler(context.Background(), "w1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.ID != "w1" {
		t.Errorf("expected wallet ID w1, got %s", w.ID)
	}
}

func TestGetWalletForHandler_NotFound(t *testing.T) {
	repo := &basicMockWalletRepo{
		wallets: make(map[string]*domain.Wallet),
	}
	svc := wallet.NewService(repo, nil, nil)

	_, err := svc.GetWalletForHandler(context.Background(), "w_missing")
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Errorf("expected ErrWalletNotFound, got %v", err)
	}
}
