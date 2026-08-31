package settlement

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/keypair"
	stellarnetwork "github.com/stellar/go/network"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

func TestBuildAsset_XLM(t *testing.T) {
	e := &Engine{assetIssuers: map[string]string{"USDC": "usdc-issuer"}}
	asset, err := e.buildAsset("XLM")
	if err != nil {
		t.Fatalf("buildAsset(XLM) error: %v", err)
	}
	if _, ok := asset.(txnbuild.NativeAsset); !ok {
		t.Fatalf("expected NativeAsset, got %T", asset)
	}
}

func TestBuildAsset_USDC(t *testing.T) {
	e := &Engine{assetIssuers: map[string]string{"USDC": "usdc-issuer-123"}}
	asset, err := e.buildAsset("USDC")
	if err != nil {
		t.Fatalf("buildAsset(USDC) error: %v", err)
	}
	ca, ok := asset.(txnbuild.CreditAsset)
	if !ok {
		t.Fatalf("expected CreditAsset, got %T", asset)
	}
	if ca.Code != "USDC" {
		t.Fatalf("code = %q, want USDC", ca.Code)
	}
	if ca.Issuer != "usdc-issuer-123" {
		t.Fatalf("issuer = %q, want usdc-issuer-123", ca.Issuer)
	}
}

func TestBuildAsset_EURC(t *testing.T) {
	e := &Engine{assetIssuers: map[string]string{
		"USDC": "usdc-issuer",
		"EURC": "eurc-issuer-456",
	}}
	asset, err := e.buildAsset("EURC")
	if err != nil {
		t.Fatalf("buildAsset(EURC) error: %v", err)
	}
	ca, ok := asset.(txnbuild.CreditAsset)
	if !ok {
		t.Fatalf("expected CreditAsset, got %T", asset)
	}
	if ca.Code != "EURC" {
		t.Fatalf("code = %q, want EURC", ca.Code)
	}
	if ca.Issuer != "eurc-issuer-456" {
		t.Fatalf("issuer = %q, want eurc-issuer-456", ca.Issuer)
	}
}

func TestBuildAsset_Unsupported(t *testing.T) {
	e := &Engine{assetIssuers: map[string]string{"USDC": "usdc-issuer"}}
	_, err := e.buildAsset("DOGE")
	if err == nil {
		t.Fatal("expected error for unsupported asset, got nil")
	}
}

func TestBuildAsset_EmptyRegistry(t *testing.T) {
	e := &Engine{assetIssuers: map[string]string{}}
	_, err := e.buildAsset("USDC")
	if err == nil {
		t.Fatal("expected error for USDC with empty registry, got nil")
	}
}

// ---------------------------------------------------------------------------
// SubmitTransfer: atomic claim, idempotent retries, ambiguous-outcome handling
// ---------------------------------------------------------------------------

type fakeTxRepo struct {
	mu   sync.Mutex
	rows map[string]*domain.Transaction
}

func newFakeTxRepo(tx *domain.Transaction) *fakeTxRepo {
	cp := *tx
	return &fakeTxRepo{rows: map[string]*domain.Transaction{tx.ID: &cp}}
}

func (f *fakeTxRepo) Create(_ context.Context, _ *domain.Transaction) error { return nil }
func (f *fakeTxRepo) CreateWithMonthlyLimit(_ context.Context, _ *domain.Transaction, _ string, _ int, _ time.Month, _ int) error {
	return nil
}

func (f *fakeTxRepo) GetByID(_ context.Context, id string) (*domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx, ok := f.rows[id]
	if !ok {
		return nil, domain.ErrTransactionNotFound
	}
	cp := *tx
	return &cp, nil
}

// ClaimForSubmission mirrors the Postgres CAS: strictly pending -> submitted.
// A row already submitted (with or without a hash yet) is never silently
// reclaimed here — that would reopen exactly the race this method exists to
// close (see TestSubmitTransfer_ConcurrentWorkers_OnlyOneSubmits).
func (f *fakeTxRepo) ClaimForSubmission(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx, ok := f.rows[id]
	if !ok {
		return domain.ErrTransactionNotFound
	}
	if tx.Status != domain.StatusPending {
		return domain.ErrConcurrentUpdate
	}
	tx.Status = domain.StatusSubmitted
	return nil
}

func (f *fakeTxRepo) UpdateStatus(_ context.Context, id string, status domain.TransactionStatus, txHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx, ok := f.rows[id]
	if !ok {
		return domain.ErrTransactionNotFound
	}
	tx.Status = status
	if txHash != "" {
		tx.TxHash = txHash
	}
	return nil
}

func (f *fakeTxRepo) ListByWallet(_ context.Context, _ string, _, _ int) ([]*domain.Transaction, error) {
	return nil, nil
}
func (f *fakeTxRepo) UpsertByTxHash(_ context.Context, _ *domain.Transaction) error { return nil }
func (f *fakeTxRepo) ListByBatch(_ context.Context, _ string) ([]*domain.Transaction, error) {
	return nil, nil
}
func (f *fakeTxRepo) CountMonthlyTransfersByTenant(_ context.Context, _ string, _ int, _ time.Month) (int, error) {
	return 0, nil
}
func (f *fakeTxRepo) ExistsByTxHash(_ context.Context, _ string) (bool, error) { return false, nil }
func (f *fakeTxRepo) GetByIdempotencyKey(_ context.Context, _, _ string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (f *fakeTxRepo) status(id string) domain.TransactionStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[id].Status
}

func (f *fakeTxRepo) txHash(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[id].TxHash
}

type fakeWalletRepo struct {
	mu             sync.Mutex
	wallets        map[string]*domain.Wallet
	upsertBalCalls int
}

func (f *fakeWalletRepo) Create(_ context.Context, _ *domain.Wallet) error { return nil }
func (f *fakeWalletRepo) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	w, ok := f.wallets[id]
	if !ok {
		return nil, fmt.Errorf("wallet %s not found", id)
	}
	return w, nil
}
func (f *fakeWalletRepo) GetByPublicKey(_ context.Context, _ string) (*domain.Wallet, error) {
	return nil, fmt.Errorf("not found")
}
func (f *fakeWalletRepo) List(_ context.Context, _, _ int) ([]*domain.Wallet, error) { return nil, nil }
func (f *fakeWalletRepo) CountByTenant(_ context.Context, _ string) (int, error)     { return 0, nil }
func (f *fakeWalletRepo) UpsertBalance(_ context.Context, _, _, _ string, _ decimal.Decimal) error {
	f.mu.Lock()
	f.upsertBalCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeWalletRepo) GetBalances(_ context.Context, _ string) ([]domain.BalanceRecord, error) {
	return nil, nil
}
func (f *fakeWalletRepo) UpdateSyncCursor(_ context.Context, _, _ string) error { return nil }

type fakeFeesService struct{
	mu             sync.Mutex
	recordColCalls int
}

func (f *fakeFeesService) GetSchedule(_ context.Context, _ string) (*domain.FeeSchedule, error) {
	return nil, nil
}
func (f *fakeFeesService) CalculateTransferFee(_ context.Context, _, _ string, _ decimal.Decimal) (*fees.TransferFee, error) {
	return nil, nil
}
func (f *fakeFeesService) CalculateConversionFee(_ context.Context, _, _ string, _ decimal.Decimal) (*fees.TransferFee, error) {
	return nil, nil
}
func (f *fakeFeesService) RecordCollection(_ context.Context, _ *domain.FeeCollection) error {
	f.mu.Lock()
	f.recordColCalls++
	f.mu.Unlock()
	return nil
}
func (f *fakeFeesService) ListCollectedSummary(_ context.Context, _, _ *time.Time) ([]domain.FeeCollectionSummary, error) {
	return nil, nil
}

type identitySigner struct{}

func (identitySigner) Sign(tx *txnbuild.Transaction, _ string) (*txnbuild.Transaction, error) {
	return tx, nil
}

// fakeStellarClient implements stellar.Client. submitFunc/txDetailFunc are
// swapped per test to simulate success, definite rejection, or an ambiguous
// (timeout/5xx-class) network error.
type fakeStellarClient struct {
	mu           sync.Mutex
	submitCount  int
	sequence     int64
	submitFunc   func(tx *txnbuild.Transaction) (horizon.Transaction, error)
	txDetailFunc func(hash string) (horizon.Transaction, error)
}

func (f *fakeStellarClient) LoadAccount(accountID string) (horizon.Account, error) {
	return horizon.Account{AccountID: accountID, Sequence: f.sequence}, nil
}
func (f *fakeStellarClient) SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	f.mu.Lock()
	f.submitCount++
	f.mu.Unlock()
	return f.submitFunc(tx)
}
func (f *fakeStellarClient) FindPathsStrict(_, _, _, _ string) ([]horizon.Path, error) {
	return nil, nil
}
func (f *fakeStellarClient) TransactionDetail(hash string) (horizon.Transaction, error) {
	if f.txDetailFunc == nil {
		return horizon.Transaction{}, fmt.Errorf("not found")
	}
	return f.txDetailFunc(hash)
}
func (f *fakeStellarClient) OperationsForTransaction(_ string) ([]operations.Operation, error) {
	return nil, nil
}
func (f *fakeStellarClient) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}
func (f *fakeStellarClient) Payments(_, _ string, _ uint) ([]operations.Operation, error) {
	return nil, nil
}
func (f *fakeStellarClient) StreamPayments(_ context.Context, _, _ string, _ func(operations.Operation) error) error {
	return nil
}
func (f *fakeStellarClient) Offers(_ string, _ uint) ([]horizon.Offer, error) { return nil, nil }

func (f *fakeStellarClient) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.submitCount
}

func testWallets(t *testing.T) (src, dst *domain.Wallet) {
	t.Helper()
	srcKP, err := keypair.Random()
	if err != nil {
		t.Fatalf("generate src keypair: %v", err)
	}
	dstKP, err := keypair.Random()
	if err != nil {
		t.Fatalf("generate dst keypair: %v", err)
	}
	return &domain.Wallet{ID: "src-wallet", PublicKey: srcKP.Address(), EncryptedSecret: "00"},
		&domain.Wallet{ID: "dst-wallet", PublicKey: dstKP.Address(), EncryptedSecret: "00"}
}

func newTestEngine(txRepo *fakeTxRepo, walletRepo *fakeWalletRepo, feeSvc *fakeFeesService, stellarClient *fakeStellarClient) *Engine {
	if feeSvc == nil {
		feeSvc = &fakeFeesService{}
	}
	return NewEngine(txRepo, walletRepo, feeSvc, stellarClient, identitySigner{}, "testnet", nil, "")
}

func alwaysSucceeds() func(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	return func(tx *txnbuild.Transaction) (horizon.Transaction, error) {
		hash, _ := tx.HashHex(stellarnetwork.TestNetworkPassphrase)
		return horizon.Transaction{Hash: hash, Successful: true}, nil
	}
}

func TestSubmitTransfer_Success_MarksConfirmed(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	feeSvc := &fakeFeesService{}
	stl := &fakeStellarClient{submitFunc: alwaysSucceeds()}
	e := newTestEngine(txRepo, walletRepo, feeSvc, stl)

	if err := e.SubmitTransfer(context.Background(), "tx-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := txRepo.status("tx-1"); got != domain.StatusConfirmed {
		t.Errorf("expected confirmed, got %s", got)
	}
	if txRepo.txHash("tx-1") == "" {
		t.Error("expected tx hash to be recorded")
	}
	if stl.calls() != 1 {
		t.Errorf("expected exactly 1 submit call, got %d", stl.calls())
	}
	if walletRepo.upsertBalCalls == 0 {
		t.Error("expected UpsertBalance to be called")
	}
	// Fee is zero, so RecordCollection might be skipped. Let's create a tx with a fee for testing it.
}

func TestSubmitTransfer_Success_CollectsFees(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-fee", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.NewFromInt(1)}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	feeSvc := &fakeFeesService{}
	stl := &fakeStellarClient{submitFunc: alwaysSucceeds()}
	e := newTestEngine(txRepo, walletRepo, feeSvc, stl)

	if err := e.SubmitTransfer(context.Background(), "tx-fee"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feeSvc.recordColCalls != 1 {
		t.Errorf("expected exactly 1 fee collection record, got %d", feeSvc.recordColCalls)
	}
}

// TestSubmitTransfer_ConcurrentWorkers_OnlyOneSubmits is the settlement
// analogue of the refresh-token and fiat-webhook races fixed elsewhere in
// this org: two workers picking up the same pending transaction must not
// both submit it to Stellar. Only the atomic ClaimForSubmission transition
// may let one of them through.
func TestSubmitTransfer_ConcurrentWorkers_OnlyOneSubmits(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	stl := &fakeStellarClient{submitFunc: alwaysSucceeds()}
	e := newTestEngine(txRepo, walletRepo, nil, stl)

	const workers = 8
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = e.SubmitTransfer(context.Background(), "tx-1")
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected error from a concurrent worker: %v", err)
		}
	}

	if stl.calls() != 1 {
		t.Fatalf("expected exactly 1 Stellar submission across %d concurrent workers, got %d", workers, stl.calls())
	}
	if got := txRepo.status("tx-1"); got != domain.StatusConfirmed {
		t.Errorf("expected confirmed, got %s", got)
	}
}

func TestSubmitTransfer_AlreadyClaimed_Skips(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusSubmitted, TxHash: "already-in-flight", FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	stl := &fakeStellarClient{submitFunc: alwaysSucceeds()}
	e := newTestEngine(txRepo, walletRepo, nil, stl)

	if err := e.SubmitTransfer(context.Background(), "tx-1"); err != nil {
		t.Fatalf("expected a graceful skip (nil), got error: %v", err)
	}
	if stl.calls() != 0 {
		t.Errorf("expected no Stellar submission for an already-claimed transaction, got %d", stl.calls())
	}
	if got := txRepo.status("tx-1"); got != domain.StatusSubmitted {
		t.Errorf("status must be left untouched, got %s", got)
	}
}

func TestSubmitTransfer_DefiniteRejection_MarksFailed(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	stl := &fakeStellarClient{submitFunc: func(_ *txnbuild.Transaction) (horizon.Transaction, error) {
		return horizon.Transaction{}, errors.New("horizon: 400 tx_bad_auth")
	}}
	e := newTestEngine(txRepo, walletRepo, nil, stl)

	if err := e.SubmitTransfer(context.Background(), "tx-1"); err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := txRepo.status("tx-1"); got != domain.StatusFailed {
		t.Errorf("expected failed for a definite rejection, got %s", got)
	}
	if stl.calls() != 1 {
		t.Errorf("a non-retryable rejection must not be retried, got %d calls", stl.calls())
	}
}

// TestSubmitTransfer_AmbiguousOutcome_NeverMarkedFailed covers the core
// requirement: when every attempt fails with a network-class (ambiguous)
// error, the transaction must NOT be marked failed — its on-chain outcome
// is unknown. It stays `submitted` with its hash recorded, ready for
// reconciliation to resolve later by looking that hash up on Horizon.
func TestSubmitTransfer_AmbiguousOutcome_NeverMarkedFailed(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	stl := &fakeStellarClient{
		submitFunc: func(_ *txnbuild.Transaction) (horizon.Transaction, error) {
			return horizon.Transaction{}, errors.New("request timeout")
		},
		// Horizon doesn't know about the hash yet — genuinely inconclusive.
		txDetailFunc: func(_ string) (horizon.Transaction, error) {
			return horizon.Transaction{}, errors.New("404 not found")
		},
	}
	e := newTestEngine(txRepo, walletRepo, nil, stl)

	err := e.SubmitTransfer(context.Background(), "tx-1")
	if err == nil {
		t.Fatal("expected an error surfaced for the ambiguous outcome, got nil")
	}
	if got := txRepo.status("tx-1"); got != domain.StatusSubmitted {
		t.Errorf("ambiguous outcome must never be marked failed; got status %s", got)
	}
	if txRepo.txHash("tx-1") == "" {
		t.Error("expected the hash to be persisted so reconciliation can resolve it")
	}
	if stl.calls() != 3 {
		t.Errorf("expected all 3 retry attempts (all ambiguous), got %d", stl.calls())
	}
}

func TestSubmitTransfer_AmbiguousOutcome_ResolvedConfirmedByHashLookup(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	stl := &fakeStellarClient{
		submitFunc: func(_ *txnbuild.Transaction) (horizon.Transaction, error) {
			// The client never saw a response, but it actually landed.
			return horizon.Transaction{}, errors.New("request timeout")
		},
		txDetailFunc: func(hash string) (horizon.Transaction, error) {
			return horizon.Transaction{Hash: hash, Successful: true}, nil
		},
	}
	e := newTestEngine(txRepo, walletRepo, nil, stl)

	if err := e.SubmitTransfer(context.Background(), "tx-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := txRepo.status("tx-1"); got != domain.StatusConfirmed {
		t.Errorf("expected confirmed once resolved by hash lookup, got %s", got)
	}
}

func TestSubmitTransfer_AmbiguousOutcome_ResolvedFailedByHashLookup(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}
	stl := &fakeStellarClient{
		submitFunc: func(_ *txnbuild.Transaction) (horizon.Transaction, error) {
			return horizon.Transaction{}, errors.New("request timeout")
		},
		txDetailFunc: func(hash string) (horizon.Transaction, error) {
			return horizon.Transaction{Hash: hash, Successful: false}, nil
		},
	}
	e := newTestEngine(txRepo, walletRepo, nil, stl)

	if err := e.SubmitTransfer(context.Background(), "tx-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := txRepo.status("tx-1"); got != domain.StatusFailed {
		t.Errorf("expected failed once resolved by hash lookup, got %s", got)
	}
}

// TestSubmitTransfer_RetryReusesSameEnvelope proves the retry loop is
// idempotent at the protocol level: every attempt submits the exact same
// signed transaction hash, never a freshly-built (and therefore differently
// sequenced) one — so a retried submission can never become a second,
// distinct on-chain payment.
func TestSubmitTransfer_RetryReusesSameEnvelope(t *testing.T) {
	src, dst := testWallets(t)
	tx := &domain.Transaction{ID: "tx-1", Status: domain.StatusPending, FromWallet: src.ID, ToWallet: dst.ID, Asset: "XLM", Amount: decimal.NewFromInt(10), Fee: decimal.Zero}
	txRepo := newFakeTxRepo(tx)
	walletRepo := &fakeWalletRepo{wallets: map[string]*domain.Wallet{src.ID: src, dst.ID: dst}}

	var mu sync.Mutex
	var seenHashes []string
	attempt := 0
	stl := &fakeStellarClient{
		submitFunc: func(tx *txnbuild.Transaction) (horizon.Transaction, error) {
			hash, _ := tx.HashHex(stellarnetwork.TestNetworkPassphrase)
			mu.Lock()
			seenHashes = append(seenHashes, hash)
			mu.Unlock()
			attempt++
			if attempt < 3 {
				return horizon.Transaction{}, errors.New("503 service unavailable")
			}
			return horizon.Transaction{Hash: hash, Successful: true}, nil
		},
	}
	e := newTestEngine(txRepo, walletRepo, nil, stl)

	if err := e.SubmitTransfer(context.Background(), "tx-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seenHashes) != 3 {
		t.Fatalf("expected 3 submit attempts, got %d", len(seenHashes))
	}
	for _, h := range seenHashes {
		if h != seenHashes[0] {
			t.Fatalf("expected every retry to reuse the same envelope/hash, got %v", seenHashes)
		}
	}
}
