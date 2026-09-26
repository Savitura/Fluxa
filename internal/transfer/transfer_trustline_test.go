package transfer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/base"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

type mockWalletRepo struct {
	wallets map[string]*domain.Wallet
}

func (m *mockWalletRepo) Create(ctx context.Context, w *domain.Wallet) error { return nil }
func (m *mockWalletRepo) GetByID(ctx context.Context, id string) (*domain.Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return w, nil
}
func (m *mockWalletRepo) GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error) {
	return nil, nil
}
func (m *mockWalletRepo) List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error) {
	return nil, nil
}
func (m *mockWalletRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	return 0, nil
}
func (m *mockWalletRepo) UpsertBalance(ctx context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error {
	return nil
}
func (m *mockWalletRepo) GetBalances(ctx context.Context, walletID string) ([]domain.BalanceRecord, error) {
	return nil, nil
}
func (m *mockWalletRepo) UpdateSyncCursor(ctx context.Context, walletID, cursor string) error {
	return nil
}

type mockTxRepo struct{}

func (m *mockTxRepo) Create(ctx context.Context, tx *domain.Transaction) error { return nil }
func (m *mockTxRepo) CreateWithMonthlyLimit(ctx context.Context, tx *domain.Transaction, tenantID string, year int, month time.Month, limit int) error {
	return nil
}
func (m *mockTxRepo) GetByID(ctx context.Context, id string) (*domain.Transaction, error) {
	return nil, nil
}
func (m *mockTxRepo) ClaimForSubmission(ctx context.Context, id string) error {
	return nil
}
func (m *mockTxRepo) UpdateStatus(ctx context.Context, id string, status domain.TransactionStatus, txHash string) error {
	return nil
}
func (m *mockTxRepo) UpsertByTxHash(ctx context.Context, tx *domain.Transaction) error {
	return nil
}
func (m *mockTxRepo) ListByWallet(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *mockTxRepo) ExistsByTxHash(ctx context.Context, txHash string) (bool, error) {
	return false, nil
}
func (m *mockTxRepo) GetByIdempotencyKey(ctx context.Context, orgID, idempotencyKey string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}
func (m *mockTxRepo) CountMonthlyTransfersByTenant(ctx context.Context, tenantID string, year int, month time.Month) (int, error) {
	return 0, nil
}
func (m *mockTxRepo) ListByBatch(ctx context.Context, batchID string) ([]*domain.Transaction, error) {
	return nil, nil
}

type mockStellarClient struct {
	balances []horizon.Balance
}

func (m *mockStellarClient) LoadAccount(accountID string) (horizon.Account, error) {
	return horizon.Account{Balances: m.balances}, nil
}
func (m *mockStellarClient) SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *mockStellarClient) FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error) {
	return nil, nil
}
func (m *mockStellarClient) TransactionDetail(hash string) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *mockStellarClient) OperationsForTransaction(hash string) ([]operations.Operation, error) {
	return nil, nil
}
func (*mockStellarClient) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}

func (m *mockStellarClient) Payments(accountID, cursor string, limit uint) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellarClient) StreamPayments(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error {
	return nil
}
func (m *mockStellarClient) Offers(accountID string, limit uint) ([]horizon.Offer, error) {
	return nil, nil
}

type mockFeeRepo struct{}

func (m *mockFeeRepo) GetSchedule(ctx context.Context, tenantID *string, asset string) (*domain.FeeSchedule, error) {
	return &domain.FeeSchedule{TransferFeeBps: 10}, nil
}
func (m *mockFeeRepo) RecordCollection(ctx context.Context, fc *domain.FeeCollection) error {
	return nil
}
func (m *mockFeeRepo) ListCollected(ctx context.Context, start, end *time.Time) ([]*domain.FeeCollection, error) {
	return nil, nil
}

func TestTransferMissingTrustlineReturns422Error(t *testing.T) {
	wRepo := &mockWalletRepo{
		wallets: map[string]*domain.Wallet{
			"w-from": {ID: "w-from", PublicKey: "GBSRC123"},
			"w-to":   {ID: "w-to", PublicKey: "GBDST456"},
		},
	}
	txRepo := &mockTxRepo{}
	feeSvc := fees.NewService(&mockFeeRepo{})

	// Stellar account has only XLM balance, no USDC trustline
	stClient := &mockStellarClient{
		balances: []horizon.Balance{
			{Asset: base.Asset{Code: ""}, Balance: "100.0000000"},
		},
	}

	svc := transfer.NewService(txRepo, wRepo, feeSvc, nil).WithStellarClient(stClient)

	_, err := svc.InitiateTransfer(context.Background(), "w-from", "w-to", "USDC", decimal.NewFromInt(10))
	if err == nil {
		t.Fatal("expected error when transferring USDC without trustline, got nil")
	}

	var noTL *domain.ErrNoTrustline
	if !errors.As(err, &noTL) {
		t.Fatalf("expected domain.ErrNoTrustline error, got %v", err)
	}

	if noTL.Error() != "Source wallet has no trustline for USDC" {
		t.Errorf("unexpected error message: %s", noTL.Error())
	}
}

func TestTransferXLMRequiresNoTrustline(t *testing.T) {
	wRepo := &mockWalletRepo{
		wallets: map[string]*domain.Wallet{
			"w-from": {ID: "w-from", PublicKey: "GBSRC123"},
			"w-to":   {ID: "w-to", PublicKey: "GBDST456"},
		},
	}
	txRepo := &mockTxRepo{}
	feeSvc := fees.NewService(&mockFeeRepo{})

	stClient := &mockStellarClient{
		balances: []horizon.Balance{
			{Asset: base.Asset{Code: ""}, Balance: "100.0000000"},
		},
	}

	svc := transfer.NewService(txRepo, wRepo, feeSvc, nil).WithStellarClient(stClient)

	tx, err := svc.InitiateTransfer(context.Background(), "w-from", "w-to", "XLM", decimal.NewFromInt(10))
	if err != nil {
		t.Fatalf("unexpected error initiating XLM transfer: %v", err)
	}
	if tx == nil || tx.Asset != "XLM" {
		t.Fatalf("expected valid XLM transaction, got %v", tx)
	}
}
