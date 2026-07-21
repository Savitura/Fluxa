package indexer

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
	horizonclient "github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/base"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

type balanceUpsert struct {
	walletID, assetCode, issuer string
	balance                     decimal.Decimal
}

type fakeWalletRepo struct {
	wallets  map[string]*domain.Wallet
	balances []balanceUpsert
	cursors  map[string]string
}

func newFakeWalletRepo() *fakeWalletRepo {
	return &fakeWalletRepo{wallets: make(map[string]*domain.Wallet), cursors: make(map[string]string)}
}

func (f *fakeWalletRepo) Create(_ context.Context, w *domain.Wallet) error {
	f.wallets[w.ID] = w
	return nil
}

func (f *fakeWalletRepo) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	w, ok := f.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return w, nil
}

func (f *fakeWalletRepo) GetByPublicKey(_ context.Context, pubKey string) (*domain.Wallet, error) {
	for _, w := range f.wallets {
		if w.PublicKey == pubKey {
			return w, nil
		}
	}
	return nil, domain.ErrWalletNotFound
}

func (f *fakeWalletRepo) List(_ context.Context, limit, offset int) ([]*domain.Wallet, error) {
	var out []*domain.Wallet
	for _, w := range f.wallets {
		out = append(out, w)
	}
	return out, nil
}

func (f *fakeWalletRepo) UpsertBalance(_ context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error {
	f.balances = append(f.balances, balanceUpsert{walletID, assetCode, issuer, balance})
	return nil
}

func (f *fakeWalletRepo) UpdateSyncCursor(_ context.Context, walletID, cursor string) error {
	f.cursors[walletID] = cursor
	return nil
}

type fakeTransferRepo struct {
	created  []*domain.Transaction
	existing map[string]bool
}

func newFakeTransferRepo() *fakeTransferRepo {
	return &fakeTransferRepo{existing: make(map[string]bool)}
}

func (f *fakeTransferRepo) Create(_ context.Context, tx *domain.Transaction) error {
	f.created = append(f.created, tx)
	f.existing[tx.TxHash] = true
	return nil
}

func (f *fakeTransferRepo) GetByID(_ context.Context, id string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (f *fakeTransferRepo) UpdateStatus(_ context.Context, id string, status domain.TransactionStatus, txHash string) error {
	return nil
}

func (f *fakeTransferRepo) ListByWallet(_ context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTransferRepo) ListByBatch(_ context.Context, batchID string) ([]*domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTransferRepo) ExistsByTxHash(_ context.Context, txHash string) (bool, error) {
	return f.existing[txHash], nil
}

type fakeStellarClient struct {
	loadAccount    func(accountID string) (horizon.Account, error)
	payments       func(accountID, cursor string, limit uint) ([]operations.Operation, error)
	streamPayments func(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error
}

func (f *fakeStellarClient) LoadAccount(accountID string) (horizon.Account, error) {
	if f.loadAccount != nil {
		return f.loadAccount(accountID)
	}
	return horizon.Account{}, nil
}

func (f *fakeStellarClient) SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}

func (f *fakeStellarClient) FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error) {
	return nil, nil
}

func (f *fakeStellarClient) TransactionDetail(hash string) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}

func (f *fakeStellarClient) OperationsForTransaction(hash string) ([]operations.Operation, error) {
	return nil, nil
}

func (f *fakeStellarClient) Payments(accountID, cursor string, limit uint) ([]operations.Operation, error) {
	if f.payments != nil {
		return f.payments(accountID, cursor, limit)
	}
	return nil, nil
}

func (f *fakeStellarClient) StreamPayments(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error {
	if f.streamPayments != nil {
		return f.streamPayments(ctx, accountID, cursor, handler)
	}
	<-ctx.Done()
	return nil
}

func incomingPaymentOp(id, pagingToken, txHash, to, amount string) operations.Payment {
	return operations.Payment{
		Base: operations.Base{
			ID:                    id,
			PT:                    pagingToken,
			Type:                  "payment",
			TransactionHash:       txHash,
			TransactionSuccessful: true,
		},
		Asset:  base.Asset{Type: "native"},
		From:   "GSOURCEACCOUNT",
		To:     to,
		Amount: amount,
	}
}

func TestSyncWallet_PersistsBalancesAndRecordsIncomingPayment(t *testing.T) {
	w := &domain.Wallet{ID: "wallet-1", PublicKey: "GDEST"}
	walletRepo := newFakeWalletRepo()
	walletRepo.wallets[w.ID] = w
	txRepo := newFakeTransferRepo()

	op := incomingPaymentOp("op-1", "12345", "hash-1", "GDEST", "42.5000000")

	stellarClient := &fakeStellarClient{
		loadAccount: func(accountID string) (horizon.Account, error) {
			acct := horizon.Account{}
			acct.Balances = []horizon.Balance{
				{Balance: "100.0000000", Asset: base.Asset{Type: "native"}},
			}
			return acct, nil
		},
		payments: func(accountID, cursor string, limit uint) ([]operations.Operation, error) {
			if cursor != "" {
				return nil, nil
			}
			return []operations.Operation{op}, nil
		},
	}

	idx := New(walletRepo, txRepo, stellarClient)
	if err := idx.SyncWallet(context.Background(), w); err != nil {
		t.Fatalf("SyncWallet() error: %v", err)
	}

	if len(walletRepo.balances) != 1 || !walletRepo.balances[0].balance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("expected XLM balance of 100 upserted, got %+v", walletRepo.balances)
	}
	if walletRepo.cursors[w.ID] != "12345" {
		t.Fatalf("cursor = %q, want %q", walletRepo.cursors[w.ID], "12345")
	}
	if len(txRepo.created) != 1 {
		t.Fatalf("expected 1 transaction created, got %d", len(txRepo.created))
	}
	tx := txRepo.created[0]
	if tx.ToWallet != w.ID || tx.TxHash != "hash-1" || tx.Asset != "XLM" || !tx.Amount.Equal(decimal.NewFromFloat(42.5)) {
		t.Fatalf("unexpected transaction: %+v", tx)
	}
}

func TestSyncWallet_SkipsAlreadyRecordedTransaction(t *testing.T) {
	w := &domain.Wallet{ID: "wallet-1", PublicKey: "GDEST"}
	walletRepo := newFakeWalletRepo()
	walletRepo.wallets[w.ID] = w
	txRepo := newFakeTransferRepo()
	txRepo.existing["hash-1"] = true // already recorded, e.g. by settlement flow or a prior sync

	op := incomingPaymentOp("op-1", "12345", "hash-1", "GDEST", "10.0000000")

	stellarClient := &fakeStellarClient{
		loadAccount: func(accountID string) (horizon.Account, error) {
			return horizon.Account{}, nil
		},
		payments: func(accountID, cursor string, limit uint) ([]operations.Operation, error) {
			if cursor != "" {
				return nil, nil
			}
			return []operations.Operation{op}, nil
		},
	}

	idx := New(walletRepo, txRepo, stellarClient)
	if err := idx.SyncWallet(context.Background(), w); err != nil {
		t.Fatalf("SyncWallet() error: %v", err)
	}

	if len(txRepo.created) != 0 {
		t.Fatalf("expected no new transactions for an already-recorded hash, got %d", len(txRepo.created))
	}
	// The cursor still advances so the duplicate isn't reconsidered on the next sync.
	if walletRepo.cursors[w.ID] != "12345" {
		t.Fatalf("cursor = %q, want %q", walletRepo.cursors[w.ID], "12345")
	}
}

func TestSyncWallet_SkipsOutgoingPayments(t *testing.T) {
	w := &domain.Wallet{ID: "wallet-1", PublicKey: "GDEST"}
	walletRepo := newFakeWalletRepo()
	walletRepo.wallets[w.ID] = w
	txRepo := newFakeTransferRepo()

	// Payment originating from this wallet (already tracked by the settlement
	// flow when it was submitted) should not be re-recorded as inbound.
	op := incomingPaymentOp("op-1", "12345", "hash-1", "GSOMEONEELSE", "10.0000000")

	stellarClient := &fakeStellarClient{
		loadAccount: func(accountID string) (horizon.Account, error) {
			return horizon.Account{}, nil
		},
		payments: func(accountID, cursor string, limit uint) ([]operations.Operation, error) {
			if cursor != "" {
				return nil, nil
			}
			return []operations.Operation{op}, nil
		},
	}

	idx := New(walletRepo, txRepo, stellarClient)
	if err := idx.SyncWallet(context.Background(), w); err != nil {
		t.Fatalf("SyncWallet() error: %v", err)
	}

	if len(txRepo.created) != 0 {
		t.Fatalf("expected outgoing payment to be skipped, got %d transactions", len(txRepo.created))
	}
}

func TestSyncWallet_AccountNotFound_ReturnsNilWithoutSyncing(t *testing.T) {
	w := &domain.Wallet{ID: "wallet-1", PublicKey: "GDEST"}
	walletRepo := newFakeWalletRepo()
	walletRepo.wallets[w.ID] = w
	txRepo := newFakeTransferRepo()

	stellarClient := &fakeStellarClient{
		loadAccount: func(accountID string) (horizon.Account, error) {
			return horizon.Account{}, &horizonclient.Error{
				Response: &http.Response{StatusCode: 404, Status: "404"},
			}
		},
	}

	idx := New(walletRepo, txRepo, stellarClient)
	if err := idx.SyncWallet(context.Background(), w); err != nil {
		t.Fatalf("SyncWallet() error: %v, want nil for an unfunded account", err)
	}

	if len(walletRepo.balances) != 0 || len(txRepo.created) != 0 {
		t.Fatal("expected no balances or transactions to be recorded for an unfunded account")
	}
}

func TestStreamWallet_ReconnectsAfterStreamFailureAndProcessesPayment(t *testing.T) {
	w := &domain.Wallet{ID: "wallet-1", PublicKey: "GDEST"}
	walletRepo := newFakeWalletRepo()
	walletRepo.wallets[w.ID] = w
	txRepo := newFakeTransferRepo()

	op := incomingPaymentOp("op-1", "99999", "hash-1", "GDEST", "5.0000000")

	var attempts int
	processed := make(chan struct{})

	stellarClient := &fakeStellarClient{
		streamPayments: func(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error {
			attempts++
			if attempts == 1 {
				return errors.New("connection reset by peer")
			}
			if err := handler(op); err != nil {
				return err
			}
			close(processed)
			<-ctx.Done()
			return nil
		},
	}

	idx := New(walletRepo, txRepo, stellarClient)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		idx.StreamWallet(ctx, w)
		close(done)
	}()

	select {
	case <-processed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream to reconnect and process a payment")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamWallet did not return after ctx was canceled")
	}

	if attempts < 2 {
		t.Fatalf("expected at least 2 stream attempts (initial + reconnect), got %d", attempts)
	}
	if len(txRepo.created) != 1 || txRepo.created[0].TxHash != "hash-1" {
		t.Fatalf("expected payment processed after reconnect, got %+v", txRepo.created)
	}
	if walletRepo.cursors[w.ID] != "99999" {
		t.Fatalf("cursor = %q, want %q", walletRepo.cursors[w.ID], "99999")
	}
}

func TestStreamWallet_StopsImmediatelyWhenContextCanceled(t *testing.T) {
	w := &domain.Wallet{ID: "wallet-1", PublicKey: "GDEST"}
	walletRepo := newFakeWalletRepo()
	walletRepo.wallets[w.ID] = w
	txRepo := newFakeTransferRepo()

	stellarClient := &fakeStellarClient{}
	idx := New(walletRepo, txRepo, stellarClient)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		idx.StreamWallet(ctx, w)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StreamWallet did not return promptly for an already-canceled context")
	}
}
