package fiat

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/shopspring/decimal"
)

// mockRepository implements fiat.Repository for testing. A mutex guards the
// deposits map so ClaimDepositForProcessing can emulate the atomicity a real
// database gives a single conditional UPDATE — required to exercise
// concurrent-webhook races deterministically instead of just racing the map.
type mockRepository struct {
	mu          sync.Mutex
	deposits    map[string]*domain.FiatDeposit
	withdrawals map[string]*domain.FiatWithdrawal
	createErr   error
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		deposits:    make(map[string]*domain.FiatDeposit),
		withdrawals: make(map[string]*domain.FiatWithdrawal),
	}
}

func (m *mockRepository) CreateDeposit(ctx context.Context, d *domain.FiatDeposit) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deposits[d.ID] = d
	return nil
}

func (m *mockRepository) UpdateDepositStatus(ctx context.Context, id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deposits[id]
	if !ok {
		return errors.New("deposit not found")
	}
	if d.Status == domain.FiatStatusCompleted || d.Status == domain.FiatStatusFailed {
		return fmt.Errorf("deposit %s already processed or terminal", id)
	}
	d.Status = status
	return nil
}

// ClaimDepositForProcessing mimics the atomic pending->processing UPDATE the
// real Postgres repository performs: it only succeeds once per deposit, so
// concurrent/duplicate callers racing on the same map entry can't both win.
func (m *mockRepository) ClaimDepositForProcessing(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deposits[id]
	if !ok {
		return errors.New("deposit not found")
	}
	if d.Status != domain.FiatStatusPending {
		return fmt.Errorf("deposit %s already claimed or not pending", id)
	}
	d.Status = domain.FiatStatusProcessing
	return nil
}

func (m *mockRepository) GetDepositByReference(ctx context.Context, ref string) (*domain.FiatDeposit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.deposits {
		if d.ProviderReference == ref {
			// Return a snapshot, like a SQL SELECT would, rather than a
			// pointer into the map: the caller reads deposit.Status without
			// going through this mutex, and the map entry keeps mutating
			// underneath ClaimDepositForProcessing/UpdateDepositStatus as
			// concurrent callers race to claim it.
			snapshot := *d
			return &snapshot, nil
		}
	}
	return nil, errors.New("deposit not found")
}

func (m *mockRepository) CreateWithdrawal(ctx context.Context, w *domain.FiatWithdrawal) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.withdrawals[w.ID] = w
	return nil
}

func (m *mockRepository) UpdateWithdrawalStatus(ctx context.Context, id, status string) error {
	if w, ok := m.withdrawals[id]; ok {
		w.Status = status
		return nil
	}
	return errors.New("withdrawal not found")
}

func (m *mockRepository) GetWithdrawalByReference(ctx context.Context, ref string) (*domain.FiatWithdrawal, error) {
	for _, w := range m.withdrawals {
		if w.ProviderReference == ref {
			return w, nil
		}
	}
	return nil, errors.New("not found")
}

// mockRail implements fiat.Rail for testing
type mockRail struct {
	withdrawResp *WithdrawResponse
	withdrawErr  error
	webhookEvt   *RailEvent
	webhookErr   error
}

func (m *mockRail) Deposit(ctx context.Context, req DepositRequest) (*DepositResponse, error) {
	return nil, nil
}

func (m *mockRail) Withdraw(ctx context.Context, req WithdrawRequest) (*WithdrawResponse, error) {
	if m.withdrawErr != nil {
		return nil, m.withdrawErr
	}
	return m.withdrawResp, nil
}

func (m *mockRail) HandleWebhook(ctx context.Context, payload []byte, signature string) (*RailEvent, error) {
	if m.webhookErr != nil {
		return nil, m.webhookErr
	}
	return m.webhookEvt, nil
}

// mockFXService implements fx.Service for testing
type mockFXService struct {
	quote *fx.Quote
	err   error
}

func (m *mockFXService) GetQuote(ctx context.Context, fromAsset, toAsset, amount string) (*fx.Quote, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.quote, nil
}

func (m *mockFXService) ExecuteConversion(ctx context.Context, walletID, quoteID string) (*domain.Conversion, error) {
	return nil, nil
}

func (m *mockFXService) GetRates(ctx context.Context, from, to string) (*fx.RateResponse, error) {
	return nil, nil
}

// mockTransferService implements transfer.Service for testing. Guarded by a
// mutex so concurrent-webhook tests can call InitiateTransfer from multiple
// goroutines without racing on the transfers slice itself.
type mockTransferService struct {
	mu          sync.Mutex
	transferErr error
	transfers   []struct {
		fromID, toID, asset string
		amount              decimal.Decimal
	}
}

func (m *mockTransferService) InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	if m.transferErr != nil {
		return nil, m.transferErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transfers = append(m.transfers, struct {
		fromID, toID, asset string
		amount              decimal.Decimal
	}{fromID, toID, asset, amount})
	return &domain.Transaction{ID: "tx-123"}, nil
}

func (m *mockTransferService) InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error) {
	return nil, nil
}

func (m *mockTransferService) InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	return nil, nil
}

func (m *mockTransferService) GetTransaction(ctx context.Context, id string) (*domain.Transaction, error) {
	return nil, nil
}

func (m *mockTransferService) ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}

func (m *mockTransferService) WithScreener(_ transfer.Screener) transfer.Service {
	return m
}

func (m *mockTransferService) WithStellarClient(stellarClient stellar.Client) transfer.Service {
	return m
}

func TestInitiateWithdrawal_Success(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{
		withdrawResp: &WithdrawResponse{Reference: "REF-123", Status: "completed"},
	}
	fxSvc := &mockFXService{
		quote: &fx.Quote{
			Rate: decimal.NewFromInt(1600),
		},
	}
	transferSvc := &mockTransferService{}

	svc := NewService(repo, rail, fxSvc, transferSvc, "platform-wallet-123", "flutterwave")

	req := WithdrawRequest{
		WalletID:      "wallet-123",
		Reference:     "REF-123",
		FiatAmount:    decimal.NewFromInt(16000), // 16000 NGN
		FiatCurrency:  "NGN",
		AccountBank:   "044",
		AccountNumber: "0123456789",
	}

	resp, err := svc.InitiateWithdrawal(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Reference != "REF-123" {
		t.Errorf("expected reference REF-123, got %s", resp.Reference)
	}

	// 16000 NGN / 1600 NGN/USDC = 10 USDC
	expectedUSDCAmount := decimal.NewFromInt(10)

	// Verify withdrawal record was created with correct USDC amount
	var createdWithdrawal *domain.FiatWithdrawal
	for _, w := range repo.withdrawals {
		createdWithdrawal = w
		break
	}

	if createdWithdrawal == nil {
		t.Fatal("withdrawal record was not created")
	}

	if !createdWithdrawal.USDCAmount.Equal(expectedUSDCAmount) {
		t.Errorf("expected USDC amount %s, got %s", expectedUSDCAmount, createdWithdrawal.USDCAmount)
	}

	// Verify transfer was initiated with correct details
	if len(transferSvc.transfers) != 1 {
		t.Fatalf("expected 1 transfer, got %d", len(transferSvc.transfers))
	}

	tr := transferSvc.transfers[0]
	if tr.fromID != "wallet-123" || tr.toID != "platform-wallet-123" || tr.asset != "USDC" || !tr.amount.Equal(expectedUSDCAmount) {
		t.Errorf("unexpected transfer details: %+v", tr)
	}
}

func TestInitiateWithdrawal_FXServiceFailure(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{}
	fxSvc := &mockFXService{
		err: errors.New("FX provider down"),
	}
	transferSvc := &mockTransferService{}

	svc := NewService(repo, rail, fxSvc, transferSvc, "platform-wallet-123", "flutterwave")

	req := WithdrawRequest{
		WalletID:     "wallet-123",
		Reference:    "REF-123",
		FiatAmount:   decimal.NewFromInt(16000),
		FiatCurrency: "NGN",
	}

	_, err := svc.InitiateWithdrawal(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if len(repo.withdrawals) != 0 {
		t.Errorf("expected 0 withdrawals in repo, got %d", len(repo.withdrawals))
	}

	if len(transferSvc.transfers) != 0 {
		t.Errorf("expected 0 transfers, got %d", len(transferSvc.transfers))
	}
}

func TestInitiateWithdrawal_FXZeroRate(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{}
	fxSvc := &mockFXService{
		quote: &fx.Quote{
			Rate: decimal.Zero,
		},
	}
	transferSvc := &mockTransferService{}

	svc := NewService(repo, rail, fxSvc, transferSvc, "platform-wallet-123", "flutterwave")

	req := WithdrawRequest{
		WalletID:     "wallet-123",
		Reference:    "REF-123",
		FiatAmount:   decimal.NewFromInt(16000),
		FiatCurrency: "NGN",
	}

	_, err := svc.InitiateWithdrawal(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if len(repo.withdrawals) != 0 {
		t.Errorf("expected 0 withdrawals in repo, got %d", len(repo.withdrawals))
	}
}

func TestInitiateWithdrawal_TransferFailure(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{}
	fxSvc := &mockFXService{
		quote: &fx.Quote{
			Rate: decimal.NewFromInt(1600),
		},
	}
	transferSvc := &mockTransferService{
		transferErr: errors.New("insufficient balance"),
	}

	svc := NewService(repo, rail, fxSvc, transferSvc, "platform-wallet-123", "flutterwave")

	req := WithdrawRequest{
		WalletID:     "wallet-123",
		Reference:    "REF-123",
		FiatAmount:   decimal.NewFromInt(16000),
		FiatCurrency: "NGN",
	}

	_, err := svc.InitiateWithdrawal(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify withdrawal record was created and its status updated to Failed
	if len(repo.withdrawals) != 1 {
		t.Fatalf("expected 1 withdrawal in repo, got %d", len(repo.withdrawals))
	}

	var w *domain.FiatWithdrawal
	for _, val := range repo.withdrawals {
		w = val
		break
	}

	if w.Status != domain.FiatStatusFailed {
		t.Errorf("expected withdrawal status to be Failed, got %s", w.Status)
	}
}

func TestInitiateWithdrawal_RailFailure(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{
		withdrawErr: errors.New("rail endpoint timeout"),
	}
	fxSvc := &mockFXService{
		quote: &fx.Quote{
			Rate: decimal.NewFromInt(1600),
		},
	}
	transferSvc := &mockTransferService{}

	svc := NewService(repo, rail, fxSvc, transferSvc, "platform-wallet-123", "flutterwave")

	req := WithdrawRequest{
		WalletID:     "wallet-123",
		Reference:    "REF-123",
		FiatAmount:   decimal.NewFromInt(16000),
		FiatCurrency: "NGN",
	}

	_, err := svc.InitiateWithdrawal(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify withdrawal record was created and its status updated to Failed
	if len(repo.withdrawals) != 1 {
		t.Fatalf("expected 1 withdrawal in repo, got %d", len(repo.withdrawals))
	}

	var w *domain.FiatWithdrawal
	for _, val := range repo.withdrawals {
		w = val
		break
	}

	if w.Status != domain.FiatStatusFailed {
		t.Errorf("expected withdrawal status to be Failed, got %s", w.Status)
	}
}

// ─── HandleWebhook: deposit crediting ─────────────────────────────────────────

func seedPendingDeposit(repo *mockRepository, ref string, amount decimal.Decimal, currency string) *domain.FiatDeposit {
	d := &domain.FiatDeposit{
		ID:                "deposit-" + ref,
		WalletID:          "wallet-123",
		Provider:          "flutterwave",
		ProviderReference: ref,
		FiatAmount:        amount,
		FiatCurrency:      currency,
		USDCAmount:        decimal.NewFromInt(10),
		Status:            domain.FiatStatusPending,
	}
	repo.deposits[d.ID] = d
	return d
}

func TestHandleWebhook_Deposit_Success(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, "REF-1", decimal.NewFromInt(16000), "NGN")
	transferSvc := &mockTransferService{}
	rail := &mockRail{webhookEvt: &RailEvent{
		Type:        EventDepositConfirmed,
		ProviderRef: "REF-1",
		Status:      "completed",
		Amount:      decimal.NewFromInt(16000),
		Currency:    "NGN",
	}}

	svc := NewService(repo, rail, &mockFXService{}, transferSvc, "platform-wallet-123", "flutterwave")

	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(transferSvc.transfers) != 1 {
		t.Fatalf("expected 1 transfer, got %d", len(transferSvc.transfers))
	}
	if repo.deposits["deposit-REF-1"].Status != domain.FiatStatusCompleted {
		t.Errorf("expected deposit status completed, got %s", repo.deposits["deposit-REF-1"].Status)
	}
}

func TestHandleWebhook_Deposit_AmountMismatch_Rejected(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, "REF-1", decimal.NewFromInt(16000), "NGN")
	transferSvc := &mockTransferService{}
	rail := &mockRail{webhookEvt: &RailEvent{
		Type:        EventDepositConfirmed,
		ProviderRef: "REF-1",
		Status:      "completed",
		Amount:      decimal.NewFromInt(1), // attacker-controlled deposit paid ~nothing
		Currency:    "NGN",
	}}

	svc := NewService(repo, rail, &mockFXService{}, transferSvc, "platform-wallet-123", "flutterwave")

	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err == nil {
		t.Fatal("expected error for amount mismatch, got nil")
	}

	if len(transferSvc.transfers) != 0 {
		t.Fatalf("expected no transfer on amount mismatch, got %d", len(transferSvc.transfers))
	}
	if repo.deposits["deposit-REF-1"].Status != domain.FiatStatusPending {
		t.Errorf("deposit must stay pending on amount mismatch, got %s", repo.deposits["deposit-REF-1"].Status)
	}
}

func TestHandleWebhook_Deposit_CurrencyMismatch_Rejected(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, "REF-1", decimal.NewFromInt(16000), "NGN")
	transferSvc := &mockTransferService{}
	rail := &mockRail{webhookEvt: &RailEvent{
		Type:        EventDepositConfirmed,
		ProviderRef: "REF-1",
		Status:      "completed",
		Amount:      decimal.NewFromInt(16000),
		Currency:    "USD", // wrong currency for the same numeric amount
	}}

	svc := NewService(repo, rail, &mockFXService{}, transferSvc, "platform-wallet-123", "flutterwave")

	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err == nil {
		t.Fatal("expected error for currency mismatch, got nil")
	}
	if len(transferSvc.transfers) != 0 {
		t.Fatalf("expected no transfer on currency mismatch, got %d", len(transferSvc.transfers))
	}
}

func TestHandleWebhook_Deposit_UnknownReference_Errors(t *testing.T) {
	repo := newMockRepository()
	transferSvc := &mockTransferService{}
	rail := &mockRail{webhookEvt: &RailEvent{
		Type:        EventDepositConfirmed,
		ProviderRef: "does-not-exist",
		Status:      "completed",
		Amount:      decimal.NewFromInt(100),
		Currency:    "NGN",
	}}

	svc := NewService(repo, rail, &mockFXService{}, transferSvc, "platform-wallet-123", "flutterwave")

	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err == nil {
		t.Fatal("expected error for unknown reference, got nil")
	}
}

// TestHandleWebhook_Deposit_Replay_NoDoubleCredit simulates a provider
// re-delivering the exact same webhook after it already succeeded (a common
// retry behavior, and also what a captured-and-replayed payload looks like).
// The second delivery must be a no-op, not a second credit.
func TestHandleWebhook_Deposit_Replay_NoDoubleCredit(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, "REF-1", decimal.NewFromInt(16000), "NGN")
	transferSvc := &mockTransferService{}
	rail := &mockRail{webhookEvt: &RailEvent{
		Type:        EventDepositConfirmed,
		ProviderRef: "REF-1",
		Status:      "completed",
		Amount:      decimal.NewFromInt(16000),
		Currency:    "NGN",
	}}
	svc := NewService(repo, rail, &mockFXService{}, transferSvc, "platform-wallet-123", "flutterwave")

	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err != nil {
		t.Fatalf("unexpected error on first delivery: %v", err)
	}
	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err != nil {
		t.Fatalf("replayed delivery should be a silent no-op, got error: %v", err)
	}

	if len(transferSvc.transfers) != 1 {
		t.Fatalf("expected exactly 1 transfer across both deliveries, got %d", len(transferSvc.transfers))
	}
}

// TestHandleWebhook_Deposit_ConcurrentDelivery_OnlyCreditsOnce is the direct
// analogue of the refresh-token race this codebase already fixed elsewhere:
// two webhook deliveries for the same event arrive close enough together
// that both read the deposit as still "pending" before either has updated
// it. Only the atomic ClaimDepositForProcessing step (checked before any
// funds move) may allow exactly one of them through.
func TestHandleWebhook_Deposit_ConcurrentDelivery_OnlyCreditsOnce(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, "REF-1", decimal.NewFromInt(16000), "NGN")
	transferSvc := &mockTransferService{}
	rail := &mockRail{webhookEvt: &RailEvent{
		Type:        EventDepositConfirmed,
		ProviderRef: "REF-1",
		Status:      "completed",
		Amount:      decimal.NewFromInt(16000),
		Currency:    "NGN",
	}}
	svc := NewService(repo, rail, &mockFXService{}, transferSvc, "platform-wallet-123", "flutterwave")

	const attempts = 8
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.HandleWebhook(context.Background(), []byte("{}"), "sig")
		}(i)
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected error from a concurrent delivery: %v", err)
		}
	}

	transferSvc.mu.Lock()
	transferCount := len(transferSvc.transfers)
	transferSvc.mu.Unlock()

	if transferCount != 1 {
		t.Fatalf("expected exactly 1 transfer across %d concurrent deliveries, got %d", attempts, transferCount)
	}
	if repo.deposits["deposit-REF-1"].Status != domain.FiatStatusCompleted {
		t.Errorf("expected deposit status completed, got %s", repo.deposits["deposit-REF-1"].Status)
	}
}
