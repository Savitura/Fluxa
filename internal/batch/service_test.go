package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// fakeBatchRepo implements Repository for testing.
type fakeBatchRepo struct {
	batches map[string]*domain.Batch
}

func newFakeBatchRepo() *fakeBatchRepo {
	return &fakeBatchRepo{batches: make(map[string]*domain.Batch)}
}

func (f *fakeBatchRepo) Create(_ context.Context, b *domain.Batch) error {
	f.batches[b.ID] = b
	return nil
}

func (f *fakeBatchRepo) GetByID(_ context.Context, id string) (*domain.Batch, error) {
	b, ok := f.batches[id]
	if !ok {
		return nil, domain.ErrBatchNotFound
	}
	return b, nil
}

// fakeTxRepo implements transfer.Repository, storing transactions by batch.
type fakeTxRepo struct {
	byBatch map[string][]*domain.Transaction
}

func newFakeTxRepo() *fakeTxRepo {
	return &fakeTxRepo{byBatch: make(map[string][]*domain.Transaction)}
}

func (f *fakeTxRepo) Create(_ context.Context, tx *domain.Transaction) error {
	if tx.BatchID != nil {
		f.byBatch[*tx.BatchID] = append(f.byBatch[*tx.BatchID], tx)
	}
	return nil
}

func (f *fakeTxRepo) GetByID(_ context.Context, id string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (f *fakeTxRepo) UpdateStatus(_ context.Context, id string, status domain.TransactionStatus, txHash string) error {
	return nil
}

func (f *fakeTxRepo) ListByWallet(_ context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTxRepo) ListByBatch(_ context.Context, batchID string) ([]*domain.Transaction, error) {
	return f.byBatch[batchID], nil
}

// fakeTransferSvc implements transfer.Service. It simulates the settlement
// worker having already confirmed each transfer synchronously, except for
// destinations listed in failOn, which behave like a submission failure.
type fakeTransferSvc struct {
	txRepo *fakeTxRepo
	failOn map[string]bool
	calls  int
}

func (f *fakeTransferSvc) InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	return f.InitiateBatchTransfer(ctx, fromID, toID, asset, amount, "", "")
}

func (f *fakeTransferSvc) InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	f.calls++
	if f.failOn[toID] {
		return nil, errors.New("destination account does not exist")
	}

	tx := &domain.Transaction{
		ID:         uuid.New().String(),
		Type:       domain.TypeTransfer,
		Status:     domain.StatusConfirmed,
		FromWallet: fromID,
		ToWallet:   toID,
		Asset:      asset,
		Amount:     amount,
		TxHash:     "hash-" + toID,
		Reference:  reference,
		CreatedAt:  time.Now().UTC(),
	}
	if batchID != "" {
		tx.BatchID = &batchID
	}
	_ = f.txRepo.Create(ctx, tx)
	return tx, nil
}

func (f *fakeTransferSvc) GetTransaction(_ context.Context, id string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (f *fakeTransferSvc) ListTransactions(_ context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}

func makeItems(n int, failIndex int) ([]Item, map[string]bool) {
	items := make([]Item, n)
	failOn := make(map[string]bool)
	for i := 0; i < n; i++ {
		wallet := fmt.Sprintf("wallet-%d", i)
		items[i] = Item{
			ToWalletID: wallet,
			Asset:      "XLM",
			Amount:     decimal.NewFromInt(10),
			Reference:  fmt.Sprintf("ref-%d", i),
		}
		if i == failIndex {
			failOn[wallet] = true
		}
	}
	return items, failOn
}

func TestCreateBatch_LinksFiveTransactionsToBatch(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)

	items, _ := makeItems(5, -1)
	result, err := svc.CreateBatch(context.Background(), "source-wallet", items)
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}
	if result.Batch.TotalCount != 5 {
		t.Fatalf("TotalCount = %d, want 5", result.Batch.TotalCount)
	}
	if len(result.Transactions) != 5 {
		t.Fatalf("got %d transactions, want 5", len(result.Transactions))
	}
	for _, tx := range result.Transactions {
		if tx.BatchID == nil || *tx.BatchID != result.Batch.ID {
			t.Fatalf("transaction %s not linked to batch %s", tx.ID, result.Batch.ID)
		}
	}
}

func TestCreateBatch_OneOfFiveFails_BatchIsPartialAndOthersSucceed(t *testing.T) {
	txRepo := newFakeTxRepo()
	items, failOn := makeItems(5, 2)
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: failOn}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)

	created, err := svc.CreateBatch(context.Background(), "source-wallet", items)
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}

	result, err := svc.GetBatch(context.Background(), created.Batch.ID)
	if err != nil {
		t.Fatalf("GetBatch() error: %v", err)
	}

	if result.Batch.Status != domain.BatchStatusPartial {
		t.Fatalf("status = %s, want %s", result.Batch.Status, domain.BatchStatusPartial)
	}

	var succeeded, failed int
	for _, tx := range result.Transactions {
		switch tx.Status {
		case domain.StatusConfirmed:
			succeeded++
		case domain.StatusFailed:
			failed++
		}
	}
	if succeeded != 4 {
		t.Fatalf("succeeded = %d, want 4", succeeded)
	}
	if failed != 1 {
		t.Fatalf("failed = %d, want 1", failed)
	}
}

func TestCreateBatch_AllSucceed_BatchIsCompleted(t *testing.T) {
	txRepo := newFakeTxRepo()
	items, _ := makeItems(3, -1)
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)

	created, _ := svc.CreateBatch(context.Background(), "source-wallet", items)
	result, err := svc.GetBatch(context.Background(), created.Batch.ID)
	if err != nil {
		t.Fatalf("GetBatch() error: %v", err)
	}
	if result.Batch.Status != domain.BatchStatusCompleted {
		t.Fatalf("status = %s, want %s", result.Batch.Status, domain.BatchStatusCompleted)
	}
}

func TestCreateBatch_AllFail_BatchIsFailed(t *testing.T) {
	txRepo := newFakeTxRepo()
	items, _ := makeItems(2, -1)
	failOn := map[string]bool{"wallet-0": true, "wallet-1": true}
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: failOn}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)

	created, _ := svc.CreateBatch(context.Background(), "source-wallet", items)
	result, err := svc.GetBatch(context.Background(), created.Batch.ID)
	if err != nil {
		t.Fatalf("GetBatch() error: %v", err)
	}
	if result.Batch.Status != domain.BatchStatusFailed {
		t.Fatalf("status = %s, want %s", result.Batch.Status, domain.BatchStatusFailed)
	}
}

func TestCreateBatch_RejectsEmptyAndOversizedBatches(t *testing.T) {
	txRepo := newFakeTxRepo()
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)

	if _, err := svc.CreateBatch(context.Background(), "source-wallet", nil); !errors.Is(err, domain.ErrBatchEmpty) {
		t.Fatalf("empty batch error = %v, want ErrBatchEmpty", err)
	}

	items, _ := makeItems(101, -1)
	if _, err := svc.CreateBatch(context.Background(), "source-wallet", items); !errors.Is(err, domain.ErrBatchTooLarge) {
		t.Fatalf("oversized batch error = %v, want ErrBatchTooLarge", err)
	}
}

func TestExportCSV_IncludesStatusTxHashAndReference(t *testing.T) {
	txRepo := newFakeTxRepo()
	items, _ := makeItems(2, -1)
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: map[string]bool{}}
	svc := NewService(newFakeBatchRepo(), txRepo, transferSvc)

	created, _ := svc.CreateBatch(context.Background(), "source-wallet", items)
	csv, err := svc.ExportCSV(context.Background(), created.Batch.ID)
	if err != nil {
		t.Fatalf("ExportCSV() error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if lines[0] != "to_wallet,asset,amount,reference,status,tx_hash" {
		t.Fatalf("header = %q", lines[0])
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows)", len(lines))
	}
	if !strings.Contains(lines[1], "ref-0") || !strings.Contains(lines[1], "confirmed") || !strings.Contains(lines[1], "hash-wallet-0") {
		t.Fatalf("row missing expected fields: %q", lines[1])
	}
}
