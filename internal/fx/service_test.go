package fx

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockWalletRepo struct {
	wallets map[string]*domain.Wallet
}

func newMockWalletRepo(wallets ...*domain.Wallet) *mockWalletRepo {
	m := &mockWalletRepo{wallets: make(map[string]*domain.Wallet)}
	for _, w := range wallets {
		m.wallets[w.ID] = w
	}
	return m
}

func (m *mockWalletRepo) Create(_ context.Context, w *domain.Wallet) error { return nil }
func (m *mockWalletRepo) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return w, nil
}
func (m *mockWalletRepo) GetByPublicKey(_ context.Context, _ string) (*domain.Wallet, error) {
	return nil, domain.ErrWalletNotFound
}
func (m *mockWalletRepo) List(_ context.Context, _, _ int) ([]*domain.Wallet, error) {
	return nil, nil
}
func (m *mockWalletRepo) CountByTenant(_ context.Context, _ string) (int, error) { return 0, nil }
func (m *mockWalletRepo) UpsertBalance(_ context.Context, _, _, _ string, _ decimal.Decimal) error {
	return nil
}
func (m *mockWalletRepo) GetBalances(_ context.Context, _ string) ([]domain.BalanceRecord, error) {
	return nil, nil
}
func (m *mockWalletRepo) UpdateSyncCursor(_ context.Context, _, _ string) error { return nil }

type mockConvRepo struct {
	conversions map[string]*domain.Conversion
}

func newMockConvRepo() *mockConvRepo {
	return &mockConvRepo{conversions: make(map[string]*domain.Conversion)}
}

func (m *mockConvRepo) Create(_ context.Context, c *domain.Conversion) error {
	m.conversions[c.ID] = c
	return nil
}

type mockAuditRepo struct{}

func (m *mockAuditRepo) CreateQuote(_ context.Context, _ *Quote) error      { return nil }
func (m *mockAuditRepo) MarkQuoteUsed(_ context.Context, _, _ string) error { return nil }

type mockFeeSvc struct{}

func (m *mockFeeSvc) GetSchedule(_ context.Context, _ string) (*domain.FeeSchedule, error) {
	return nil, nil
}
func (m *mockFeeSvc) CalculateTransferFee(_ context.Context, _, _ string, _ decimal.Decimal) (*fees.TransferFee, error) {
	return &fees.TransferFee{FeeAmount: decimal.Zero, NetAmount: decimal.NewFromInt(10), FeeBps: 0}, nil
}
func (m *mockFeeSvc) CalculateConversionFee(_ context.Context, _, _ string, _ decimal.Decimal) (*fees.TransferFee, error) {
	return &fees.TransferFee{FeeAmount: decimal.Zero, NetAmount: decimal.NewFromInt(10), FeeBps: 0}, nil
}
func (m *mockFeeSvc) RecordCollection(_ context.Context, _ *domain.FeeCollection) error { return nil }
func (m *mockFeeSvc) ListCollectedSummary(_ context.Context, _, _ *time.Time) ([]domain.FeeCollectionSummary, error) {
	return nil, nil
}

type mockStellar struct{}

func (m *mockStellar) LoadAccount(_ string) (horizon.Account, error) {
	return horizon.Account{}, nil
}
func (m *mockStellar) SubmitTransaction(_ *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *mockStellar) FindPathsStrict(_, _, _, _ string) ([]horizon.Path, error) {
	return nil, nil
}
func (m *mockStellar) TransactionDetail(_ string) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *mockStellar) OperationsForTransaction(_ string) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellar) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}

func (m *mockStellar) Payments(_, _ string, _ uint) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellar) StreamPayments(_ context.Context, _, _ string, _ func(operations.Operation) error) error {
	return nil
}
func (m *mockStellar) Offers(_ string, _ uint) ([]horizon.Offer, error) {
	return nil, nil
}

// Verify mock satisfies interface at compile time.
var _ = (*mockStellar)(nil)

type mockProvider struct {
	rate decimal.Decimal
}

func (m *mockProvider) GetRate(_ context.Context, _, _, _ string) (decimal.Decimal, error) {
	return m.rate, nil
}

func (m *mockProvider) SupportedPairs() []string {
	return []string{"USDC-XLM", "XLM-USDC"}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func ptrString(s string) *string { return &s }

func tenantCtx(tenantID string) context.Context {
	return tenant.WithID(context.Background(), tenantID)
}

func setupService(t *testing.T, mr *miniredis.Miniredis) Service {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewService(
		newMockWalletRepo(),
		newMockConvRepo(),
		&mockAuditRepo{},
		&mockFeeSvc{},
		&mockStellar{},
		rdb,
		"usdc-issuer",
		[]Provider{&mockProvider{rate: decimal.NewFromInt(2)}}, // 1 USDC = 2 XLM
		100, // 100 bps spread
	)
}

// storeQuoteJSON writes a Quote directly into miniredis so ExecuteConversion
// can read it via the Lua script.
func storeQuoteJSON(t *testing.T, mr *miniredis.Miniredis, q *Quote) {
	t.Helper()
	data, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal quote: %v", err)
	}
	mr.Set(quoteKeyPrefix+q.ID, string(data))
}

// ---------------------------------------------------------------------------
// GetQuote tests
// ---------------------------------------------------------------------------

func TestGetQuote_Success(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr)
	q, err := svc.GetQuote(tenantCtx("org-1"), "USDC", "XLM", "10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.OrgID != "org-1" {
		t.Errorf("OrgID = %q, want %q", q.OrgID, "org-1")
	}
	if q.FromAmount.Sign() <= 0 {
		t.Errorf("FromAmount should be positive, got %s", q.FromAmount)
	}
	if q.ToAmount.Sign() <= 0 {
		t.Errorf("ToAmount should be positive, got %s", q.ToAmount)
	}
}

func TestGetQuote_NegativeAmount(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr)
	_, err = svc.GetQuote(tenantCtx("org-1"), "USDC", "XLM", "-5")
	if !errors.Is(err, domain.ErrInvalidQuoteAmount) {
		t.Errorf("expected ErrInvalidQuoteAmount, got %v", err)
	}
}

func TestGetQuote_ZeroAmount(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr)
	_, err = svc.GetQuote(tenantCtx("org-1"), "USDC", "XLM", "0")
	if !errors.Is(err, domain.ErrInvalidQuoteAmount) {
		t.Errorf("expected ErrInvalidQuoteAmount, got %v", err)
	}
}

func TestGetQuote_InvalidAmount(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr)
	_, err = svc.GetQuote(tenantCtx("org-1"), "USDC", "XLM", "abc")
	if !errors.Is(err, domain.ErrInvalidAsset) {
		t.Errorf("expected ErrInvalidAsset, got %v", err)
	}
}

func TestGetQuote_EmptyAmount(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr)
	_, err = svc.GetQuote(tenantCtx("org-1"), "USDC", "XLM", "")
	if err == nil {
		t.Error("expected error for empty amount")
	}
}

// ---------------------------------------------------------------------------
// ExecuteConversion tests
// ---------------------------------------------------------------------------

func walletPtr(id, tenantID string) *domain.Wallet {
	return &domain.Wallet{ID: id, TenantID: ptrString(tenantID), PublicKey: "G" + id}
}

func TestExecuteConversion_Success(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := walletPtr("w-1", "org-1")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	wr := newMockWalletRepo(w)
	cr := newMockConvRepo()
	svc := NewService(wr, cr, &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	q := &Quote{
		ID:         "q-ok-1",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.NewFromInt(10),
		ToAmount:   decimal.NewFromInt(20),
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Second),
		Used:       false,
	}
	storeQuoteJSON(t, mr, q)

	conv, err := svc.ExecuteConversion(tenantCtx("org-1"), "w-1", "q-ok-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv.WalletID != "w-1" {
		t.Errorf("WalletID = %q, want %q", conv.WalletID, "w-1")
	}
	if conv.SourceAsset != "USDC" {
		t.Errorf("SourceAsset = %q, want USDC", conv.SourceAsset)
	}
	if conv.DestAsset != "XLM" {
		t.Errorf("DestAsset = %q, want XLM", conv.DestAsset)
	}
	if len(cr.conversions) != 1 {
		t.Errorf("expected 1 conversion persisted, got %d", len(cr.conversions))
	}
}

func TestExecuteConversion_WalletNotFound(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-missing", "q-1")
	if !errors.Is(err, domain.ErrWalletNotFound) {
		t.Errorf("expected ErrWalletNotFound, got %v", err)
	}
}

func TestExecuteConversion_CrossTenant(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	// Wallet belongs to org-2
	w := walletPtr("w-2", "org-2")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	// Quote belongs to org-1
	q := &Quote{
		ID:         "q-foreign",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.NewFromInt(10),
		ToAmount:   decimal.NewFromInt(20),
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Second),
		Used:       false,
	}
	storeQuoteJSON(t, mr, q)

	_, err = svc.ExecuteConversion(tenantCtx("org-2"), "w-2", "q-foreign")
	if !errors.Is(err, domain.ErrQuoteOwnershipMismatch) {
		t.Errorf("expected ErrQuoteOwnershipMismatch, got %v", err)
	}
}

func TestExecuteConversion_QuoteExpired(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := walletPtr("w-1", "org-1")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	q := &Quote{
		ID:         "q-expired",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.NewFromInt(10),
		ToAmount:   decimal.NewFromInt(20),
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(-1 * time.Minute), // already expired
		Used:       false,
	}
	storeQuoteJSON(t, mr, q)

	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-1", "q-expired")
	if !errors.Is(err, domain.ErrQuoteExpired) {
		t.Errorf("expected ErrQuoteExpired, got %v", err)
	}
}

func TestExecuteConversion_QuoteAlreadyUsed(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := walletPtr("w-1", "org-1")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	q := &Quote{
		ID:         "q-used",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.NewFromInt(10),
		ToAmount:   decimal.NewFromInt(20),
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Second),
		Used:       true, // already used
	}
	storeQuoteJSON(t, mr, q)

	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-1", "q-used")
	if !errors.Is(err, domain.ErrQuoteAlreadyUsed) {
		t.Errorf("expected ErrQuoteAlreadyUsed, got %v", err)
	}
}

func TestExecuteConversion_NonPositiveAmountInQuote(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := walletPtr("w-1", "org-1")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	q := &Quote{
		ID:         "q-neg",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.NewFromInt(-10), // tampered negative amount
		ToAmount:   decimal.NewFromInt(20),
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Second),
		Used:       false,
	}
	storeQuoteJSON(t, mr, q)

	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-1", "q-neg")
	if !errors.Is(err, domain.ErrInvalidQuoteAmount) {
		t.Errorf("expected ErrInvalidQuoteAmount, got %v", err)
	}
}

func TestExecuteConversion_NonPositiveToAmountInQuote(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := walletPtr("w-1", "org-1")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	q := &Quote{
		ID:         "q-neg-to",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.NewFromInt(10),
		ToAmount:   decimal.NewFromInt(-20), // tampered negative dest amount
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Second),
		Used:       false,
	}
	storeQuoteJSON(t, mr, q)

	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-1", "q-neg-to")
	if !errors.Is(err, domain.ErrInvalidQuoteAmount) {
		t.Errorf("expected ErrInvalidQuoteAmount, got %v", err)
	}
}

func TestExecuteConversion_ZeroFromAmountInQuote(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := walletPtr("w-1", "org-1")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	q := &Quote{
		ID:         "q-zero",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.Zero, // zero amount
		ToAmount:   decimal.NewFromInt(20),
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Second),
		Used:       false,
	}
	storeQuoteJSON(t, mr, q)

	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-1", "q-zero")
	if !errors.Is(err, domain.ErrInvalidQuoteAmount) {
		t.Errorf("expected ErrInvalidQuoteAmount, got %v", err)
	}
}

func TestExecuteConversion_WalletNilTenantID(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	// Wallet with nil TenantID
	w := &domain.Wallet{ID: "w-nil", TenantID: nil, PublicKey: "Gw-nil"}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	q := &Quote{
		ID:         "q-nil-tenant",
		OrgID:      "org-1",
		FromAsset:  "USDC",
		ToAsset:    "XLM",
		FromAmount: decimal.NewFromInt(10),
		ToAmount:   decimal.NewFromInt(20),
		Rate:       decimal.NewFromInt(2),
		Fee:        decimal.Zero,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Second),
		Used:       false,
	}
	storeQuoteJSON(t, mr, q)

	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-nil", "q-nil-tenant")
	if !errors.Is(err, domain.ErrQuoteOwnershipMismatch) {
		t.Errorf("expected ErrQuoteOwnershipMismatch for nil TenantID, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Active pair eviction tests
// ---------------------------------------------------------------------------

func TestEvictStalePairs_RemovesOnlyStaleEntries(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr).(*service)
	svc.registerActivePair("USDC:XLM")
	svc.registerActivePair("XLM:USDC")

	// Backdate one pair so it looks idle beyond maxAge.
	svc.activePairsMu.Lock()
	svc.activePairs["USDC:XLM"] = time.Now().Add(-1 * time.Hour)
	svc.activePairsMu.Unlock()

	svc.evictStalePairs(10 * time.Minute)

	svc.activePairsMu.RLock()
	defer svc.activePairsMu.RUnlock()
	if _, ok := svc.activePairs["USDC:XLM"]; ok {
		t.Error("expected stale pair USDC:XLM to be evicted")
	}
	if _, ok := svc.activePairs["XLM:USDC"]; !ok {
		t.Error("expected recently queried pair XLM:USDC to be retained")
	}
}

func TestRegisterActivePair_RefreshesLastQueried(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr).(*service)
	svc.activePairsMu.Lock()
	svc.activePairs["USDC:XLM"] = time.Now().Add(-1 * time.Hour)
	svc.activePairsMu.Unlock()

	svc.registerActivePair("USDC:XLM")

	svc.evictStalePairs(10 * time.Minute)

	svc.activePairsMu.RLock()
	defer svc.activePairsMu.RUnlock()
	if _, ok := svc.activePairs["USDC:XLM"]; !ok {
		t.Error("expected re-registered pair to survive eviction")
	}
}

func TestExecuteConversion_QuoteNotFoundInRedis(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	w := walletPtr("w-1", "org-1")
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	svc := NewService(newMockWalletRepo(w), newMockConvRepo(), &mockAuditRepo{}, &mockFeeSvc{}, &mockStellar{}, rdb, "usdc-issuer", nil, 0)

	// Don't store any quote — the Lua script should return QUOTE_EXPIRED
	_, err = svc.ExecuteConversion(tenantCtx("org-1"), "w-1", "q-missing")
	if !errors.Is(err, domain.ErrQuoteExpired) {
		t.Errorf("expected ErrQuoteExpired for missing quote, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetRates tests
// ---------------------------------------------------------------------------

func TestGetRates_Success(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	svc := setupService(t, mr)

	// Since mockProvider returns rate=2.0 for everything, and supports USDC-XLM
	rates, err := svc.GetRates(context.Background(), "USDC", "XLM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	
	if rates.Rate.Cmp(decimal.NewFromInt(2)) != 0 {
		t.Errorf("expected rate 2, got %v", rates.Rate)
	}
}

