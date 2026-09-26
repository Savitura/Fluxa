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
)

type basicMockWalletRepo struct {
	wallets map[string]*domain.Wallet
}

func (m *basicMockWalletRepo) Create(ctx context.Context, w *domain.Wallet) error { return nil }
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

type basicMockTxRepo struct {
	txs []*domain.Transaction
}

func (m *basicMockTxRepo) Create(ctx context.Context, tx *domain.Transaction) error {
	m.txs = append(m.txs, tx)
	return nil
}
func (m *basicMockTxRepo) CreateWithMonthlyLimit(ctx context.Context, tx *domain.Transaction, tenantID string, year int, month time.Month, limit int) error {
	return nil
}
func (m *basicMockTxRepo) GetByID(ctx context.Context, id string) (*domain.Transaction, error) {
	return nil, nil
}
func (m *basicMockTxRepo) ClaimForSubmission(ctx context.Context, id string) error {
	return nil
}
func (m *basicMockTxRepo) UpdateStatus(ctx context.Context, id string, status domain.TransactionStatus, txHash string) error {
	return nil
}
func (m *basicMockTxRepo) UpsertByTxHash(ctx context.Context, tx *domain.Transaction) error {
	return nil
}
func (m *basicMockTxRepo) ListByWallet(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return m.txs, nil
}
func (m *basicMockTxRepo) ExistsByTxHash(ctx context.Context, txHash string) (bool, error) {
	return false, nil
}
func (m *basicMockTxRepo) GetByIdempotencyKey(ctx context.Context, orgID, idempotencyKey string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}
func (m *basicMockTxRepo) ListByBatch(ctx context.Context, batchID string) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *basicMockTxRepo) CountMonthlyTransfersByTenant(ctx context.Context, tenantID string, year int, month time.Month) (int, error) {
	return 0, nil
}

type basicMockFeeSvc struct{}

func (m *basicMockFeeSvc) GetSchedule(ctx context.Context, orgID string) (*domain.FeeSchedule, error) {
	return nil, nil
}
func (m *basicMockFeeSvc) CalculateTransferFee(ctx context.Context, orgID, asset string, amount decimal.Decimal) (*fees.TransferFee, error) {
	return &fees.TransferFee{
		FeeAmount: decimal.Zero,
		NetAmount: amount,
		FeeBps:    0,
	}, nil
}
func (m *basicMockFeeSvc) CalculateConversionFee(ctx context.Context, orgID, asset string, amount decimal.Decimal) (*fees.TransferFee, error) {
	return nil, nil
}
func (m *basicMockFeeSvc) RecordCollection(ctx context.Context, collection *domain.FeeCollection) error {
	return nil
}
func (m *basicMockFeeSvc) ListCollectedSummary(ctx context.Context, orgID string, since *time.Time) ([]domain.FeeCollectionSummary, error) {
	return nil, nil
}

func TestInitiateTransfer_Success(t *testing.T) {
	wr := &basicMockWalletRepo{
		wallets: map[string]*domain.Wallet{
			"w1": {ID: "w1", PublicKey: "G1"},
			"w2": {ID: "w2", PublicKey: "G2"},
		},
	}
	tr := &basicMockTxRepo{}
	feeSvc := &basicMockFeeSvc{}
	
	svc := transfer.NewService(tr, wr, feeSvc, nil)

	// Since XLM doesn't require trustline validation, it should succeed
	tx, err := svc.InitiateTransfer(context.Background(), "w1", "w2", "XLM", decimal.NewFromInt(10))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tx.Asset != "XLM" || tx.Amount.Cmp(decimal.NewFromInt(10)) != 0 {
		t.Errorf("unexpected tx values")
	}

	if len(tr.txs) != 1 {
		t.Errorf("expected transaction to be saved")
	}
}

func TestInitiateTransfer_SelfTransfer(t *testing.T) {
	wr := &basicMockWalletRepo{}
	tr := &basicMockTxRepo{}
	feeSvc := &basicMockFeeSvc{}
	
	svc := transfer.NewService(tr, wr, feeSvc, nil)

	_, err := svc.InitiateTransfer(context.Background(), "w1", "w1", "XLM", decimal.NewFromInt(10))
	if !errors.Is(err, domain.ErrSelfTransfer) {
		t.Errorf("expected ErrSelfTransfer, got %v", err)
	}
}

func TestListTransactions(t *testing.T) {
	wr := &basicMockWalletRepo{}
	tr := &basicMockTxRepo{
		txs: []*domain.Transaction{
			{ID: "tx1", FromWallet: "w1"},
			{ID: "tx2", FromWallet: "w1"},
		},
	}
	feeSvc := &basicMockFeeSvc{}
	
	svc := transfer.NewService(tr, wr, feeSvc, nil)

	txs, err := svc.ListTransactions(context.Background(), "w1", 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(txs) != 2 {
		t.Errorf("expected 2 txs, got %d", len(txs))
	}
}
