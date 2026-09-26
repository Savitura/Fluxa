package reconcile

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/assets"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/base"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

// ─── Mocks ─────────────────────────────────────────────────────────────

type mockRepo struct {
	txes                []*domain.Transaction
	updateConfirmedErr  error
	updateFailedErr     error
	updateConfStatusErr error
	auditLogs           []*AuditLogEntry
}

func (m *mockRepo) GetConfirmedTxesForReconciliation(_ context.Context, _ time.Duration) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *mockRepo) ResetStuckSubmittedToPending(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (m *mockRepo) GetStuckPendingTxes(_ context.Context, _ time.Duration) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *mockRepo) GetPendingTxesForReconciliation(_ context.Context, _ time.Duration) ([]*domain.Transaction, error) {
	return m.txes, nil
}
func (m *mockRepo) UpdateReconciliationStatus(_ context.Context, _ string, _ domain.TransactionStatus) error {
	return m.updateConfStatusErr
}
func (m *mockRepo) UpdateTxConfirmed(_ context.Context, _, _ string) error {
	return m.updateConfirmedErr
}
func (m *mockRepo) UpdateTxFailed(_ context.Context, _ string) error {
	return m.updateFailedErr
}
func (m *mockRepo) IncrementRequeueCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (m *mockRepo) UpdateReconciledAt(_ context.Context, _ string) error {
	return nil
}
func (m *mockRepo) WriteAuditLog(_ context.Context, entry *AuditLogEntry) error {
	m.auditLogs = append(m.auditLogs, entry)
	return nil
}
func (m *mockRepo) GetDailyReconciliationSummary(_ context.Context, _ int) ([]DailySummaryRow, error) {
	return nil, nil
}
func (m *mockRepo) GetPendingStuckCount(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}
func (m *mockRepo) WriteReconciliationRun(_ context.Context, _ *ReconciliationRun) error {
	return nil
}

// Compile-time interface checks.
var _ stellar.Client = (*mockStellarClient)(nil)

type mockStellarClient struct {
	txDetail    horizon.Transaction
	txDetailErr error
}

func (m *mockStellarClient) TransactionDetail(_ string) (horizon.Transaction, error) {
	return m.txDetail, m.txDetailErr
}
func (m *mockStellarClient) OperationsForTransaction(_ string) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellarClient) LoadAccount(_ string) (horizon.Account, error) {
	return horizon.Account{}, nil
}
func (m *mockStellarClient) SubmitTransaction(_ *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *mockStellarClient) FindPathsStrict(_, _, _, _ string) ([]horizon.Path, error) {
	return nil, nil
}
func (m *mockStellarClient) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}

func (m *mockStellarClient) Payments(_, _ string, _ uint) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellarClient) StreamPayments(_ context.Context, _, _ string, _ func(operations.Operation) error) error {
	return nil
}
func (m *mockStellarClient) Offers(_ string, _ uint) ([]horizon.Offer, error) {
	return nil, nil
}

// Compile-time interface check.
var _ webhook.Service = (*mockWebhookSvc)(nil)

type mockWebhookSvc struct {
	calls []webhookDispatch
}

type webhookDispatch struct {
	event   domain.EventType
	payload interface{}
}

func (m *mockWebhookSvc) Register(_ context.Context, _ string, _ []string) (*domain.WebhookEndpoint, error) {
	return nil, nil
}
func (m *mockWebhookSvc) List(_ context.Context) ([]*domain.WebhookEndpoint, error) {
	return nil, nil
}
func (m *mockWebhookSvc) Delete(_ context.Context, _ string) error {
	return nil
}
func (m *mockWebhookSvc) ListDeliveries(_ context.Context, _ string, _, _ int) ([]*domain.WebhookDelivery, error) {
	return nil, nil
}
func (m *mockWebhookSvc) Dispatch(_ context.Context, eventType domain.EventType, payload interface{}) error {
	m.calls = append(m.calls, webhookDispatch{event: eventType, payload: payload})
	return nil
}
func (m *mockWebhookSvc) Deliver(_ context.Context, _ string) error {
	return nil
}

// ─── Helpers ───────────────────────────────────────────────────────────

func pendingTx(id, hash string) *domain.Transaction {
	return &domain.Transaction{
		ID:        id,
		TxHash:    hash,
		Status:    domain.StatusPending,
		CreatedAt: time.Now().UTC().Add(-5 * time.Minute),
		Amount:    decimal.RequireFromString("100"),
		Fee:       decimal.Zero,
	}
}

func newSvc(repo Repository, stl stellar.Client, wh webhook.Service) *Service {
	return &Service{
		repo:              repo,
		stellar:           stl,
		alerting:          alerting.NewClient("", "test"),
		webhookSvc:        wh,
		assetRegistry:     assets.NewRegistry(realIssuer, ""),
		platformFeeWallet: feeWallet,
	}
}

// ─── Tests: checkPendingTransaction success paths ──────────────────────

func TestCheckPending_SuccessConfirmed(t *testing.T) {
	tx := pendingTx("tx-1", "abc123")
	stl := &mockStellarClient{txDetail: horizon.Transaction{Successful: true, Hash: "abc123", Ledger: 100}}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !disc {
		t.Fatal("expected discrepancy=true for pending→confirmed correction")
	}
	if !corr {
		t.Fatal("expected correction=true for pending→confirmed correction")
	}
	if len(wh.calls) != 1 {
		t.Fatalf("expected 1 webhook dispatch, got %d", len(wh.calls))
	}
	if wh.calls[0].event != domain.EventTransferSettled {
		t.Fatalf("expected event=%s, got %s", domain.EventTransferSettled, wh.calls[0].event)
	}
}

func TestCheckPending_SuccessFailed(t *testing.T) {
	tx := pendingTx("tx-2", "abc456")
	stl := &mockStellarClient{
		txDetail: horizon.Transaction{Successful: false, Hash: "abc456", Ledger: 100, ResultXdr: "AAAAAAAAAGQ="},
	}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !disc {
		t.Fatal("expected discrepancy=true for pending→failed correction")
	}
	if !corr {
		t.Fatal("expected correction=true for pending→failed correction")
	}
	if len(wh.calls) != 1 {
		t.Fatalf("expected 1 webhook dispatch, got %d", len(wh.calls))
	}
	if wh.calls[0].event != domain.EventTransferFailed {
		t.Fatalf("expected event=%s, got %s", domain.EventTransferFailed, wh.calls[0].event)
	}
}

// ─── Tests: zero-row update / concurrent update handling ────────────────

func TestCheckPending_ConcurrentUpdateConfirmed(t *testing.T) {
	// Both DB and Horizon say confirmed → repo returns ErrConcurrentUpdate
	// because another reconciler already flipped the row.
	tx := pendingTx("tx-3", "abc789")
	stl := &mockStellarClient{txDetail: horizon.Transaction{Successful: true, Hash: "abc789", Ledger: 100}}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{updateConfirmedErr: fmt.Errorf("update tx confirmed: %w", domain.ErrConcurrentUpdate)}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !disc {
		t.Fatal("expected discrepancy=true (Horizon confirms the tx exists)")
	}
	if corr {
		t.Fatal("expected correction=false when concurrent update was detected")
	}
	if len(wh.calls) != 0 {
		t.Fatalf("expected 0 webhook dispatches for concurrent update, got %d", len(wh.calls))
	}
}

func TestCheckPending_ConcurrentUpdateFailed(t *testing.T) {
	// Horizon says failed, but repo returns ErrConcurrentUpdate — another
	// reconciler already marked it failed.
	tx := pendingTx("tx-4", "abc012")
	stl := &mockStellarClient{
		txDetail: horizon.Transaction{Successful: false, Hash: "abc012", Ledger: 100, ResultXdr: "AAAAAAAAAGQ="},
	}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{updateFailedErr: fmt.Errorf("update tx failed: %w", domain.ErrConcurrentUpdate)}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !disc {
		t.Fatal("expected discrepancy=true (Horizon reports a result)")
	}
	if corr {
		t.Fatal("expected correction=false when concurrent update was detected")
	}
	if len(wh.calls) != 0 {
		t.Fatalf("expected 0 webhook dispatches for concurrent update, got %d", len(wh.calls))
	}
}

// ─── Tests: non-concurrent repo errors ─────────────────────────────────

func TestCheckPending_RepoErrorConfirmed(t *testing.T) {
	tx := pendingTx("tx-5", "abc345")
	stl := &mockStellarClient{txDetail: horizon.Transaction{Successful: true, Hash: "abc345", Ledger: 100}}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{updateConfirmedErr: fmt.Errorf("db connection lost")}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err == nil {
		t.Fatal("expected error for repo failure")
	}
	if !disc {
		t.Fatal("expected discrepancy=true when repo fails")
	}
	if corr {
		t.Fatal("expected correction=false when repo fails")
	}
}

func TestCheckPending_RepoErrorFailed(t *testing.T) {
	tx := pendingTx("tx-6", "abc678")
	stl := &mockStellarClient{
		txDetail: horizon.Transaction{Successful: false, Hash: "abc678", Ledger: 100, ResultXdr: "AAAAAAAAAGQ="},
	}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{updateFailedErr: fmt.Errorf("db connection lost")}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err == nil {
		t.Fatal("expected error for repo failure")
	}
	if !disc {
		t.Fatal("expected discrepancy=true when repo fails")
	}
	if corr {
		t.Fatal("expected correction=false when repo fails")
	}
}

// ─── Tests: concurrent reconciliation (RunPendingReconciliation) ────────

func TestRunPendingReconciliation_MixedResults(t *testing.T) {
	// Three transactions: one succeeds, one concurrent update, one repo error.
	tx1 := pendingTx("tx-ok", "hash-ok")
	tx2 := pendingTx("tx-concurrent", "hash-concurrent")
	tx3 := pendingTx("tx-error", "hash-error")

	smartRepo := &smartMockRepo{
		txes: []*domain.Transaction{tx1, tx2, tx3},
		confirmedErrs: map[string]error{
			"tx-ok":         nil,
			"tx-concurrent": fmt.Errorf("update tx confirmed: %w", domain.ErrConcurrentUpdate),
			"tx-error":      fmt.Errorf("db connection lost"),
		},
		failedErrs: map[string]error{},
	}

	wrappedStellar := &hashSwitchStellar{
		responses: map[string]horizon.Transaction{
			"hash-ok":         {Successful: true, Hash: "hash-ok", Ledger: 100},
			"hash-concurrent": {Successful: true, Hash: "hash-concurrent", Ledger: 101},
			"hash-error":      {Successful: true, Hash: "hash-error", Ledger: 102},
		},
	}

	wh := &mockWebhookSvc{}
	svc := newSvc(smartRepo, wrappedStellar, wh)

	checked, disc, corr, err := svc.RunPendingReconciliation(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checked != 3 {
		t.Fatalf("expected 3 txs checked, got %d", checked)
	}
	if disc != 3 {
		t.Fatalf("expected 3 discrepancies (ok + concurrent + error), got %d", disc)
	}
	if corr != 1 {
		t.Fatalf("expected 1 correction (only the successful one), got %d", corr)
	}

	// Only the successful tx should have triggered a webhook.
	if len(wh.calls) != 1 {
		t.Fatalf("expected 1 webhook dispatch (only for successful correction), got %d", len(wh.calls))
	}
	if wh.calls[0].event != domain.EventTransferSettled {
		t.Fatalf("expected event=%s, got %s", domain.EventTransferSettled, wh.calls[0].event)
	}
}

// ─── Tests: edge cases ────────────────────────────────────────────────

func TestCheckPending_HorizonEmpty(t *testing.T) {
	// An empty/zero-value Horizon tx has Successful=false, which causes
	// the code to attempt a pending→failed correction. Verify that the
	// repo call happens and the error propagates when it fails.
	tx := pendingTx("tx-7", "abc999")
	stl := &mockStellarClient{txDetail: horizon.Transaction{}}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{updateFailedErr: fmt.Errorf("db down")}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err == nil {
		t.Fatal("expected error when repo fails on empty Horizon tx")
	}
	if !disc {
		t.Fatal("expected discrepancy=true when Horizon returns empty tx")
	}
	if corr {
		t.Fatal("expected correction=false when repo fails")
	}
}

func TestCheckPending_NoHashWithinThreshold(t *testing.T) {
	tx := pendingTx("tx-8", "")
	stl := &mockStellarClient{}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if disc {
		t.Fatal("expected discrepancy=false for pending tx without hash within threshold")
	}
	if corr {
		t.Fatal("expected correction=false for pending tx without hash")
	}
}

func TestCheckPending_NoHashExceedsStuckThreshold(t *testing.T) {
	tx := pendingTx("tx-9", "")
	tx.CreatedAt = time.Now().UTC().Add(-30 * time.Minute)
	stl := &mockStellarClient{}
	wh := &mockWebhookSvc{}
	repo := &mockRepo{}
	svc := newSvc(repo, stl, wh)

	disc, corr, err := svc.checkPendingTransaction(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !disc {
		t.Fatal("expected discrepancy=true for stuck pending tx without hash")
	}
	if corr {
		t.Fatal("expected correction=false for stuck pending tx without hash")
	}
}

// ─── Tests: duplicate webhook dispatch prevention ──────────────────────

func TestCheckPending_NoDuplicateDispatchOnConcurrentUpdate(t *testing.T) {
	// Two reconcilers race on the same tx. One succeeds, the other gets a
	// zero-row update. The second must NOT dispatch a webhook.
	tx := pendingTx("tx-race", "race-hash")

	// First reconciler: succeeds.
	wh1 := &mockWebhookSvc{}
	stl1 := &mockStellarClient{txDetail: horizon.Transaction{Successful: true, Hash: "race-hash", Ledger: 50}}
	svc1 := newSvc(&mockRepo{}, stl1, wh1)

	disc1, corr1, err1 := svc1.checkPendingTransaction(context.Background(), tx)
	if err1 != nil || !disc1 || !corr1 {
		t.Fatalf("first reconciler: disc=%v corr=%v err=%v", disc1, corr1, err1)
	}

	// Second reconciler: concurrent update (zero rows affected).
	wh2 := &mockWebhookSvc{}
	stl2 := &mockStellarClient{txDetail: horizon.Transaction{Successful: true, Hash: "race-hash", Ledger: 50}}
	repo2 := &mockRepo{updateConfirmedErr: fmt.Errorf("update tx confirmed: %w", domain.ErrConcurrentUpdate)}
	svc2 := newSvc(repo2, stl2, wh2)

	disc2, corr2, err2 := svc2.checkPendingTransaction(context.Background(), tx)
	if err2 != nil || !disc2 || corr2 {
		t.Fatalf("second reconciler: disc=%v corr=%v err=%v", disc2, corr2, err2)
	}

	// Only the first reconciler should have dispatched a webhook.
	if len(wh1.calls) != 1 {
		t.Fatalf("expected 1 webhook from first reconciler, got %d", len(wh1.calls))
	}
	if len(wh2.calls) != 0 {
		t.Fatalf("expected 0 webhooks from second reconciler, got %d", len(wh2.calls))
	}
}

// ─── smartMockRepo: per-ID error injection ─────────────────────────────

type smartMockRepo struct {
	txes          []*domain.Transaction
	confirmedErrs map[string]error
	failedErrs    map[string]error
	auditLogs     []*AuditLogEntry
}

func (m *smartMockRepo) GetConfirmedTxesForReconciliation(_ context.Context, _ time.Duration) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *smartMockRepo) ResetStuckSubmittedToPending(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (m *smartMockRepo) GetStuckPendingTxes(_ context.Context, _ time.Duration) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *smartMockRepo) GetPendingTxesForReconciliation(_ context.Context, _ time.Duration) ([]*domain.Transaction, error) {
	return m.txes, nil
}
func (m *smartMockRepo) UpdateReconciliationStatus(_ context.Context, _ string, _ domain.TransactionStatus) error {
	return nil
}
func (m *smartMockRepo) UpdateTxConfirmed(_ context.Context, id, _ string) error {
	if e, ok := m.confirmedErrs[id]; ok {
		return e
	}
	return nil
}
func (m *smartMockRepo) UpdateTxFailed(_ context.Context, id string) error {
	if e, ok := m.failedErrs[id]; ok {
		return e
	}
	return nil
}
func (m *smartMockRepo) IncrementRequeueCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (m *smartMockRepo) UpdateReconciledAt(_ context.Context, _ string) error {
	return nil
}
func (m *smartMockRepo) WriteAuditLog(_ context.Context, entry *AuditLogEntry) error {
	m.auditLogs = append(m.auditLogs, entry)
	return nil
}
func (m *smartMockRepo) GetDailyReconciliationSummary(_ context.Context, _ int) ([]DailySummaryRow, error) {
	return nil, nil
}
func (m *smartMockRepo) GetPendingStuckCount(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}
func (m *smartMockRepo) WriteReconciliationRun(_ context.Context, _ *ReconciliationRun) error {
	return nil
}

// ─── hashSwitchStellar: returns different tx detail per hash ────────────

type hashSwitchStellar struct {
	responses map[string]horizon.Transaction
}

func (s *hashSwitchStellar) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}

func (s *hashSwitchStellar) TransactionDetail(hash string) (horizon.Transaction, error) {
	if tx, ok := s.responses[hash]; ok {
		return tx, nil
	}
	return horizon.Transaction{}, nil
}
func (s *hashSwitchStellar) OperationsForTransaction(_ string) ([]operations.Operation, error) {
	return nil, nil
}
func (s *hashSwitchStellar) LoadAccount(_ string) (horizon.Account, error) {
	return horizon.Account{}, nil
}
func (s *hashSwitchStellar) SubmitTransaction(_ *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (s *hashSwitchStellar) FindPathsStrict(_, _, _, _ string) ([]horizon.Path, error) {
	return nil, nil
}
func (s *hashSwitchStellar) Payments(_, _ string, _ uint) ([]operations.Operation, error) {
	return nil, nil
}
func (s *hashSwitchStellar) StreamPayments(_ context.Context, _, _ string, _ func(operations.Operation) error) error {
	return nil
}
func (s *hashSwitchStellar) Offers(_ string, _ uint) ([]horizon.Offer, error) {
	return nil, nil
}

// ─── Mocks for balance reconciliation ──────────────────────────────────

type mockWalletRepo struct {
	wallets  []*domain.Wallet
	balances map[string]map[string]decimal.Decimal // walletID → assetKey → balance
	discreps []*BalanceDiscrepancy
}

func (m *mockWalletRepo) ListAllWallets(_ context.Context) ([]*domain.Wallet, error) {
	return m.wallets, nil
}
func (m *mockWalletRepo) GetDBBalances(_ context.Context, walletID string) (map[string]decimal.Decimal, error) {
	if m.balances == nil {
		return make(map[string]decimal.Decimal), nil
	}
	return m.balances[walletID], nil
}
func (m *mockWalletRepo) WriteBalanceDiscrepancy(_ context.Context, d *BalanceDiscrepancy) error {
	m.discreps = append(m.discreps, d)
	return nil
}

type balanceStellarClient struct {
	accounts map[string]horizon.Account // publicKey → account
}

func (s *balanceStellarClient) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}

func (s *balanceStellarClient) LoadAccount(publicKey string) (horizon.Account, error) {
	if acct, ok := s.accounts[publicKey]; ok {
		return acct, nil
	}
	return horizon.Account{}, fmt.Errorf("account not found: %s", publicKey)
}
func (s *balanceStellarClient) TransactionDetail(_ string) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (s *balanceStellarClient) OperationsForTransaction(_ string) ([]operations.Operation, error) {
	return nil, nil
}
func (s *balanceStellarClient) SubmitTransaction(_ *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (s *balanceStellarClient) FindPathsStrict(_, _, _, _ string) ([]horizon.Path, error) {
	return nil, nil
}
func (s *balanceStellarClient) Payments(_, _ string, _ uint) ([]operations.Operation, error) {
	return nil, nil
}
func (s *balanceStellarClient) StreamPayments(_ context.Context, _, _ string, _ func(operations.Operation) error) error {
	return nil
}
func (s *balanceStellarClient) Offers(_ string, _ uint) ([]horizon.Offer, error) {
	return nil, nil
}

// ─── Tests: balance reconciliation with canonical asset identity ────────

func TestCheckWalletBalance_TwoIssuersSameCodeIndependent(t *testing.T) {
	// Two issuers share asset code "USDC". DB has them as separate entries
	// keyed by "USDC:ISSUER_A" and "USDC:ISSUER_B". Horizon also has both.
	// No discrepancy should be flagged.
	issuerA := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACODEA"
	issuerB := "GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBICODEB"

	wallet := &domain.Wallet{ID: "w1", PublicKey: "GWALLETXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}

	dbBalances := map[string]decimal.Decimal{
		"USDC:" + issuerA: decimal.RequireFromString("100.0000000"),
		"USDC:" + issuerB: decimal.RequireFromString("200.0000000"),
	}

	acct := horizon.Account{
		Balances: []horizon.Balance{
			{Asset: base.Asset{Type: "credit_alphanum4", Code: "USDC", Issuer: issuerA}, Balance: "100.0000000"},
			{Asset: base.Asset{Type: "credit_alphanum4", Code: "USDC", Issuer: issuerB}, Balance: "200.0000000"},
		},
	}

	stl := &balanceStellarClient{accounts: map[string]horizon.Account{wallet.PublicKey: acct}}
	wr := &mockWalletRepo{
		wallets:  []*domain.Wallet{wallet},
		balances: map[string]map[string]decimal.Decimal{"w1": dbBalances},
	}
	svc := newSvc(&mockRepo{}, stl, &mockWebhookSvc{})
	svc.walletRepo = wr

	err := svc.checkWalletBalance(context.Background(), wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wr.discreps) != 0 {
		t.Fatalf("expected 0 discrepancies, got %d", len(wr.discreps))
	}
}

func TestCheckWalletBalance_TwoIssuersSameCodeDiscrepancyOnOne(t *testing.T) {
	// Both issuers share "USDC". Issuer A matches, but Issuer B diverges.
	// Only Issuer B should produce a discrepancy.
	issuerA := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACODEA"
	issuerB := "GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBICODEB"

	wallet := &domain.Wallet{ID: "w1", PublicKey: "GWALLETXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}

	dbBalances := map[string]decimal.Decimal{
		"USDC:" + issuerA: decimal.RequireFromString("100.0000000"),
		"USDC:" + issuerB: decimal.RequireFromString("200.0000000"),
	}

	acct := horizon.Account{
		Balances: []horizon.Balance{{Asset: base.Asset{Type: "credit_alphanum4", Code: "USDC", Issuer: issuerA}, Balance: "100.0000000"},
			// Issuer B: Horizon has 150 but DB has 200.
			{Asset: base.Asset{Type: "credit_alphanum4", Code: "USDC", Issuer: issuerB}, Balance: "150.0000000"},
		},
	}

	stl := &balanceStellarClient{accounts: map[string]horizon.Account{wallet.PublicKey: acct}}
	wr := &mockWalletRepo{
		wallets:  []*domain.Wallet{wallet},
		balances: map[string]map[string]decimal.Decimal{"w1": dbBalances},
	}
	svc := newSvc(&mockRepo{}, stl, &mockWebhookSvc{})
	svc.walletRepo = wr

	err := svc.checkWalletBalance(context.Background(), wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wr.discreps) != 1 {
		t.Fatalf("expected 1 discrepancy (only issuer B), got %d", len(wr.discreps))
	}
	d := wr.discreps[0]
	expectedAsset := "USDC:" + issuerB
	if d.Asset != expectedAsset {
		t.Fatalf("expected discrepancy asset=%q, got %q", expectedAsset, d.Asset)
	}
	if !d.DBBalance.Equal(decimal.RequireFromString("200.0000000")) {
		t.Fatalf("expected DB balance 200, got %s", d.DBBalance)
	}
	if !d.HorizonBalance.Equal(decimal.RequireFromString("150.0000000")) {
		t.Fatalf("expected Horizon balance 150, got %s", d.HorizonBalance)
	}
}

func TestCheckWalletBalance_NativeXLM(t *testing.T) {
	wallet := &domain.Wallet{ID: "w1", PublicKey: "GWALLETXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}

	dbBalances := map[string]decimal.Decimal{
		"XLM": decimal.RequireFromString("500.0000000"),
	}

	acct := horizon.Account{
		Balances: []horizon.Balance{{Asset: base.Asset{Type: "native"}, Balance: "500.0000000"}},
	}

	stl := &balanceStellarClient{accounts: map[string]horizon.Account{wallet.PublicKey: acct}}
	wr := &mockWalletRepo{
		wallets:  []*domain.Wallet{wallet},
		balances: map[string]map[string]decimal.Decimal{"w1": dbBalances},
	}
	svc := newSvc(&mockRepo{}, stl, &mockWebhookSvc{})
	svc.walletRepo = wr

	err := svc.checkWalletBalance(context.Background(), wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wr.discreps) != 0 {
		t.Fatalf("expected 0 discrepancies for matching XLM, got %d", len(wr.discreps))
	}
}

func TestCheckWalletBalance_NativeXLMDiscrepancy(t *testing.T) {
	wallet := &domain.Wallet{ID: "w1", PublicKey: "GWALLETXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}

	dbBalances := map[string]decimal.Decimal{
		"XLM": decimal.RequireFromString("500.0000000"),
	}

	acct := horizon.Account{
		Balances: []horizon.Balance{{Asset: base.Asset{Type: "native"}, Balance: "450.0000000"}},
	}

	stl := &balanceStellarClient{accounts: map[string]horizon.Account{wallet.PublicKey: acct}}
	wr := &mockWalletRepo{
		wallets:  []*domain.Wallet{wallet},
		balances: map[string]map[string]decimal.Decimal{"w1": dbBalances},
	}
	svc := newSvc(&mockRepo{}, stl, &mockWebhookSvc{})
	svc.walletRepo = wr

	err := svc.checkWalletBalance(context.Background(), wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wr.discreps) != 1 {
		t.Fatalf("expected 1 discrepancy for XLM mismatch, got %d", len(wr.discreps))
	}
	if wr.discreps[0].Asset != "XLM" {
		t.Fatalf("expected asset=\"XLM\", got %q", wr.discreps[0].Asset)
	}
}

func TestCheckWalletBalance_DBAbsentIssuerNotCollapsed(t *testing.T) {
	// DB only has USDC from issuer A. Horizon has USDC from both issuers.
	// Issuer B should appear as a discrepancy (DB=0, Horizon=50).
	issuerA := "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACODEA"
	issuerB := "GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBICODEB"

	wallet := &domain.Wallet{ID: "w1", PublicKey: "GWALLETXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"}

	dbBalances := map[string]decimal.Decimal{
		"USDC:" + issuerA: decimal.RequireFromString("100.0000000"),
	}

	acct := horizon.Account{
		Balances: []horizon.Balance{{Asset: base.Asset{Type: "credit_alphanum4", Code: "USDC", Issuer: issuerA}, Balance: "100.0000000"},
			{Asset: base.Asset{Type: "credit_alphanum4", Code: "USDC", Issuer: issuerB}, Balance: "50.0000000"},
		},
	}

	stl := &balanceStellarClient{accounts: map[string]horizon.Account{wallet.PublicKey: acct}}
	wr := &mockWalletRepo{
		wallets:  []*domain.Wallet{wallet},
		balances: map[string]map[string]decimal.Decimal{"w1": dbBalances},
	}
	svc := newSvc(&mockRepo{}, stl, &mockWebhookSvc{})
	svc.walletRepo = wr

	err := svc.checkWalletBalance(context.Background(), wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(wr.discreps) != 1 {
		t.Fatalf("expected 1 discrepancy for missing issuer B, got %d", len(wr.discreps))
	}
	d := wr.discreps[0]
	expectedAsset := "USDC:" + issuerB
	if d.Asset != expectedAsset {
		t.Fatalf("expected discrepancy asset=%q, got %q", expectedAsset, d.Asset)
	}
	if !d.DBBalance.IsZero() {
		t.Fatalf("expected DB balance 0 for absent issuer, got %s", d.DBBalance)
	}
	if !d.HorizonBalance.Equal(decimal.RequireFromString("50.0000000")) {
		t.Fatalf("expected Horizon balance 50, got %s", d.HorizonBalance)
	}
}
