package fiat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

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

type mockWebhookEventRepository struct {
	mu     sync.Mutex
	events map[string]time.Time
	claims int
}

func (m *mockWebhookEventRepository) ClaimWebhookEvent(_ context.Context, provider, eventID string, expiresAt time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := provider + ":" + eventID
	if existing, ok := m.events[key]; ok && existing.After(time.Now()) {
		return false, nil
	}
	m.events[key] = expiresAt
	m.claims++
	return true, nil
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

	quote          *FiatQuote
	quoteErr       error
	currencies     []string // defaults to NGN, KES, USD when nil
	quoteCalls     int
	lastQuoteReq   QuoteRequest
	lastDepositReq DepositRequest
}

func (m *mockRail) SupportedCurrencies() []string {
	if m.currencies != nil {
		return m.currencies
	}
	return []string{"NGN", "KES", "USD"}
}

func (m *mockRail) GetQuote(_ context.Context, req QuoteRequest) (*FiatQuote, error) {
	m.quoteCalls++
	m.lastQuoteReq = req
	if m.quoteErr != nil {
		return nil, m.quoteErr
	}
	if m.quote != nil {
		return m.quote, nil
	}
	return &FiatQuote{}, nil
}

func (m *mockRail) Deposit(ctx context.Context, req DepositRequest) (*DepositResponse, error) {
	m.lastDepositReq = req
	return &DepositResponse{Reference: req.Reference}, nil
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

func (m *mockRail) HandleWebhookWithHeaders(ctx context.Context, payload []byte, headers http.Header) (*RailEvent, error) {
	return m.HandleWebhook(ctx, payload, "")
}

// mockFXService implements fx.Service for testing
type mockFXService struct {
	quote *fx.Quote
	err   error
	calls int // GetQuote invocations; the fiat service must never call it
}

func (m *mockFXService) GetQuote(ctx context.Context, fromAsset, toAsset, amount string) (*fx.Quote, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.quote, nil
}

func (m *mockFXService) ExecuteConversion(ctx context.Context, walletID, quoteID string, minAmountOut *decimal.Decimal, maxSlippageBps *int) (*domain.Conversion, error) {
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

func (m *mockTransferService) PrepareBatchTransfer(_ context.Context, params transfer.TransferParams) (*transfer.PreparedTransfer, error) {
	return nil, nil
}

func (m *mockTransferService) PersistBatchTransfer(_ context.Context, _ *transfer.PreparedTransfer) (*domain.Transaction, error) {
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
		quote:        &FiatQuote{USDCAmount: decimal.NewFromInt(10)}, // 16000 NGN at 1600 NGN/USDC
	}
	fxSvc := &mockFXService{}
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

	// The rail prices the fiat leg: 16000 NGN -> 10 USDC.
	expectedUSDCAmount := decimal.NewFromInt(10)

	if rail.quoteCalls != 1 || rail.lastQuoteReq.Side != "withdraw" ||
		rail.lastQuoteReq.FiatCurrency != "NGN" || !rail.lastQuoteReq.FiatAmount.Equal(req.FiatAmount) {
		t.Errorf("unexpected rail quote request (calls=%d): %+v", rail.quoteCalls, rail.lastQuoteReq)
	}
	if fxSvc.calls != 0 {
		t.Errorf("FX service must not price a fiat leg, got %d calls", fxSvc.calls)
	}

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

func TestInitiateWithdrawal_QuoteFailure(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{quoteErr: errors.New("rail rate provider down")}
	fxSvc := &mockFXService{}
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

func TestInitiateWithdrawal_ZeroUSDCQuote(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{quote: &FiatQuote{USDCAmount: decimal.Zero}}
	fxSvc := &mockFXService{}
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
	rail := &mockRail{quote: &FiatQuote{USDCAmount: decimal.NewFromInt(10)}}
	fxSvc := &mockFXService{}
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
		quote:       &FiatQuote{USDCAmount: decimal.NewFromInt(10)},
	}
	fxSvc := &mockFXService{}
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

// ─── Fiat pricing: currency validation and rail-priced quotes ────────────────

func TestInitiateDeposit_PricedByRail(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{quote: &FiatQuote{USDCAmount: decimal.NewFromInt(10)}}
	fxSvc := &mockFXService{}
	svc := NewService(repo, rail, fxSvc, &mockTransferService{}, "platform-wallet-123", "flutterwave")

	req := DepositRequest{
		WalletID:     "wallet-123",
		Reference:    "DEP-1",
		FiatAmount:   decimal.NewFromInt(16000),
		FiatCurrency: "NGN",
	}
	resp, err := svc.InitiateDeposit(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Reference != "DEP-1" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if rail.quoteCalls != 1 || rail.lastQuoteReq.Side != "deposit" ||
		rail.lastQuoteReq.FiatCurrency != "NGN" || !rail.lastQuoteReq.FiatAmount.Equal(req.FiatAmount) {
		t.Errorf("unexpected rail quote request (calls=%d): %+v", rail.quoteCalls, rail.lastQuoteReq)
	}
	if fxSvc.calls != 0 {
		t.Errorf("FX service must not price a fiat leg, got %d calls", fxSvc.calls)
	}

	if len(repo.deposits) != 1 {
		t.Fatalf("expected 1 deposit record, got %d", len(repo.deposits))
	}
	for _, d := range repo.deposits {
		if !d.USDCAmount.Equal(decimal.NewFromInt(10)) {
			t.Errorf("expected USDC amount 10, got %s", d.USDCAmount)
		}
		if d.FiatCurrency != "NGN" {
			t.Errorf("expected currency NGN, got %s", d.FiatCurrency)
		}
	}
}

func TestInitiateDeposit_NormalisesCurrency(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{quote: &FiatQuote{USDCAmount: decimal.NewFromInt(10)}}
	svc := NewService(repo, rail, &mockFXService{}, &mockTransferService{}, "platform-wallet-123", "flutterwave")

	_, err := svc.InitiateDeposit(context.Background(), DepositRequest{
		WalletID: "wallet-123", Reference: "DEP-1",
		FiatAmount: decimal.NewFromInt(16000), FiatCurrency: " ngn ",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rail.lastQuoteReq.FiatCurrency != "NGN" || rail.lastDepositReq.FiatCurrency != "NGN" {
		t.Errorf("currency not normalised: quote=%q deposit=%q", rail.lastQuoteReq.FiatCurrency, rail.lastDepositReq.FiatCurrency)
	}
}

func TestInitiateDeposit_UnsupportedCurrency_RejectedBeforePricing(t *testing.T) {
	for _, cur := range []string{"XYZ", "", "USDC"} {
		t.Run("currency="+cur, func(t *testing.T) {
			repo := newMockRepository()
			rail := &mockRail{quote: &FiatQuote{USDCAmount: decimal.NewFromInt(10)}}
			fxSvc := &mockFXService{}
			svc := NewService(repo, rail, fxSvc, &mockTransferService{}, "platform-wallet-123", "flutterwave")

			_, err := svc.InitiateDeposit(context.Background(), DepositRequest{
				WalletID: "wallet-123", Reference: "DEP-1",
				FiatAmount: decimal.NewFromInt(16000), FiatCurrency: cur,
			})
			if !errors.Is(err, domain.ErrUnsupportedFiatCurrency) {
				t.Fatalf("expected ErrUnsupportedFiatCurrency, got %v", err)
			}
			if rail.quoteCalls != 0 {
				t.Errorf("rail must not be asked for a quote, got %d calls", rail.quoteCalls)
			}
			if fxSvc.calls != 0 {
				t.Errorf("FX service must not be called, got %d calls", fxSvc.calls)
			}
			if len(repo.deposits) != 0 {
				t.Errorf("expected no deposit record, got %d", len(repo.deposits))
			}
		})
	}
}

func TestInitiateDeposit_QuoteFailure(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{quoteErr: errors.New("rail rate provider down")}
	svc := NewService(repo, rail, &mockFXService{}, &mockTransferService{}, "platform-wallet-123", "flutterwave")

	_, err := svc.InitiateDeposit(context.Background(), DepositRequest{
		WalletID: "wallet-123", Reference: "DEP-1",
		FiatAmount: decimal.NewFromInt(16000), FiatCurrency: "NGN",
	})
	if err == nil || errors.Is(err, domain.ErrUnsupportedFiatCurrency) {
		t.Fatalf("expected a plain quote error, got %v", err)
	}
	if len(repo.deposits) != 0 {
		t.Errorf("expected no deposit record, got %d", len(repo.deposits))
	}
}

func TestInitiateDeposit_ZeroUSDCQuote(t *testing.T) {
	repo := newMockRepository()
	rail := &mockRail{quote: &FiatQuote{USDCAmount: decimal.Zero}}
	svc := NewService(repo, rail, &mockFXService{}, &mockTransferService{}, "platform-wallet-123", "flutterwave")

	_, err := svc.InitiateDeposit(context.Background(), DepositRequest{
		WalletID: "wallet-123", Reference: "DEP-1",
		FiatAmount: decimal.NewFromInt(16000), FiatCurrency: "NGN",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(repo.deposits) != 0 {
		t.Errorf("expected no deposit record, got %d", len(repo.deposits))
	}
}

func TestInitiateWithdrawal_UnsupportedCurrency_RejectedBeforePricing(t *testing.T) {
	for _, cur := range []string{"XYZ", "", "USDC"} {
		t.Run("currency="+cur, func(t *testing.T) {
			repo := newMockRepository()
			rail := &mockRail{quote: &FiatQuote{USDCAmount: decimal.NewFromInt(10)}}
			fxSvc := &mockFXService{}
			transferSvc := &mockTransferService{}
			svc := NewService(repo, rail, fxSvc, transferSvc, "platform-wallet-123", "flutterwave")

			_, err := svc.InitiateWithdrawal(context.Background(), WithdrawRequest{
				WalletID: "wallet-123", Reference: "REF-123",
				FiatAmount: decimal.NewFromInt(16000), FiatCurrency: cur,
			})
			if !errors.Is(err, domain.ErrUnsupportedFiatCurrency) {
				t.Fatalf("expected ErrUnsupportedFiatCurrency, got %v", err)
			}
			if rail.quoteCalls != 0 {
				t.Errorf("rail must not be asked for a quote, got %d calls", rail.quoteCalls)
			}
			if fxSvc.calls != 0 {
				t.Errorf("FX service must not be called, got %d calls", fxSvc.calls)
			}
			if len(repo.withdrawals) != 0 || len(transferSvc.transfers) != 0 {
				t.Errorf("expected no withdrawal record or transfer, got %d/%d", len(repo.withdrawals), len(transferSvc.transfers))
			}
		})
	}
}

// ─── HandleWebhook: deposit crediting ─────────────────────────────────────────

func seedPendingDeposit(repo *mockRepository, amount decimal.Decimal) {
	const ref = "REF-1"
	d := &domain.FiatDeposit{
		ID:                "deposit-" + ref,
		WalletID:          "wallet-123",
		Provider:          "flutterwave",
		ProviderReference: ref,
		FiatAmount:        amount,
		FiatCurrency:      "NGN",
		USDCAmount:        decimal.NewFromInt(10),
		Status:            domain.FiatStatusPending,
	}
	repo.deposits[d.ID] = d
}

func TestHandleWebhook_Deposit_Success(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, decimal.NewFromInt(16000))
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

func TestHandleWebhook_DuplicateEventIsClaimedOnceWithSevenDayTTL(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, decimal.NewFromInt(16000))
	events := &mockWebhookEventRepository{events: make(map[string]time.Time)}
	transferSvc := &mockTransferService{}
	rail := &mockRail{webhookEvt: &RailEvent{
		Type:        EventDepositConfirmed,
		EventID:     "flutterwave-event-1",
		ProviderRef: "REF-1",
		Status:      "completed",
		Amount:      decimal.NewFromInt(16000),
		Currency:    "NGN",
	}}
	svc := NewService(repo, rail, &mockFXService{}, transferSvc, "platform-wallet-123", "flutterwave", events)

	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err != nil {
		t.Fatalf("unexpected error on first delivery: %v", err)
	}
	if err := svc.HandleWebhook(context.Background(), []byte("{}"), "sig"); err != nil {
		t.Fatalf("unexpected error on duplicate delivery: %v", err)
	}

	if events.claims != 1 {
		t.Fatalf("expected one event claim, got %d", events.claims)
	}
	if expiresAt := events.events["flutterwave:flutterwave-event-1"]; time.Until(expiresAt) < 6*24*time.Hour {
		t.Fatalf("expected approximately seven-day TTL, expires at %s", expiresAt)
	}
	if len(transferSvc.transfers) != 1 {
		t.Fatalf("expected exactly one transfer, got %d", len(transferSvc.transfers))
	}
}

func TestHandleWebhook_Deposit_AmountMismatch_Rejected(t *testing.T) {
	repo := newMockRepository()
	seedPendingDeposit(repo, decimal.NewFromInt(16000))
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
	seedPendingDeposit(repo, decimal.NewFromInt(16000))
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
	seedPendingDeposit(repo, decimal.NewFromInt(16000))
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
	seedPendingDeposit(repo, decimal.NewFromInt(16000))
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

func (m *mockTransferService) ForceSettleTransfer(_ context.Context, _, _ string) (*domain.Transaction, error) {
	return &domain.Transaction{ID: "tx-1"}, nil
}

func (m *mockTransferService) ReconcileWallet(_ context.Context, _, _ string) (*transfer.ReconcileResult, error) {
	return &transfer.ReconcileResult{}, nil
}

func (m *mockTransferService) WithAuditLogger(_ transfer.AuditLogger) transfer.Service {
	return m
}
