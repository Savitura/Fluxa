package transfer

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Mock implementations for limit tests
// ---------------------------------------------------------------------------

// limitMockTxRepo simulates the atomic check-and-insert by using a shared
// atomic counter. CreateWithMonthlyLimit atomically increments the counter
// and rejects if the limit would be exceeded — mirroring the postgres
// implementation's behaviour.
type limitMockTxRepo struct {
	count atomic.Int64
	limit int
}

func (m *limitMockTxRepo) Create(_ context.Context, tx *domain.Transaction) error {
	return nil
}

func (m *limitMockTxRepo) CreateWithMonthlyLimit(_ context.Context, tx *domain.Transaction, _ string, _ int, _ time.Month, limit int) error {
	// Simulate atomic check-and-increment.
	for {
		current := m.count.Load()
		if current >= int64(limit) {
			return domain.ErrTransferLimitReached
		}
		if m.count.CompareAndSwap(current, current+1) {
			return nil
		}
	}
}

func (m *limitMockTxRepo) GetByID(_ context.Context, id string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}
func (m *limitMockTxRepo) ClaimForSubmission(_ context.Context, _ string) error {
	return nil
}
func (m *limitMockTxRepo) UpdateStatus(_ context.Context, _ string, _ domain.TransactionStatus, _ string) error {
	return nil
}
func (m *limitMockTxRepo) ListByWallet(_ context.Context, _ string, _, _ int) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *limitMockTxRepo) ListByBatch(_ context.Context, _ string) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *limitMockTxRepo) CountMonthlyTransfersByTenant(_ context.Context, _ string, _ int, _ time.Month) (int, error) {
	return int(m.count.Load()), nil
}
func (m *limitMockTxRepo) UpsertByTxHash(_ context.Context, _ *domain.Transaction) error {
	return nil
}

func (m *limitMockTxRepo) ExistsByTxHash(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (m *limitMockTxRepo) GetByIdempotencyKey(_ context.Context, _, _ string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

type limitMockWalletRepo struct{}

func (m *limitMockWalletRepo) Create(_ context.Context, _ *domain.Wallet) error { return nil }
func (m *limitMockWalletRepo) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	return &domain.Wallet{ID: id, PublicKey: "G" + id}, nil
}
func (m *limitMockWalletRepo) GetByPublicKey(_ context.Context, _ string) (*domain.Wallet, error) {
	return nil, domain.ErrWalletNotFound
}
func (m *limitMockWalletRepo) List(_ context.Context, _, _ int) ([]*domain.Wallet, error) {
	return nil, nil
}
func (m *limitMockWalletRepo) CountByTenant(_ context.Context, _ string) (int, error) { return 0, nil }
func (m *limitMockWalletRepo) UpsertBalance(_ context.Context, _, _, _ string, _ decimal.Decimal) error {
	return nil
}
func (m *limitMockWalletRepo) GetBalances(_ context.Context, _ string) ([]domain.BalanceRecord, error) {
	return nil, nil
}
func (m *limitMockWalletRepo) UpdateSyncCursor(_ context.Context, _, _ string) error { return nil }

type limitMockTenantRepo struct {
	tenant *domain.Tenant
}

func (m *limitMockTenantRepo) GetByID(_ context.Context, id string) (*domain.Tenant, error) {
	return m.tenant, nil
}

type limitMockFeeSvc struct{}

func (m *limitMockFeeSvc) GetSchedule(_ context.Context, _ string) (*domain.FeeSchedule, error) {
	return nil, nil
}
func (m *limitMockFeeSvc) CalculateTransferFee(_ context.Context, _, _ string, _ decimal.Decimal) (*fees.TransferFee, error) {
	return &fees.TransferFee{FeeAmount: decimal.Zero, NetAmount: decimal.NewFromInt(10), FeeBps: 0}, nil
}
func (m *limitMockFeeSvc) CalculateConversionFee(_ context.Context, _, _ string, _ decimal.Decimal) (*fees.TransferFee, error) {
	return &fees.TransferFee{FeeAmount: decimal.Zero, NetAmount: decimal.NewFromInt(10), FeeBps: 0}, nil
}
func (m *limitMockFeeSvc) RecordCollection(_ context.Context, _ *domain.FeeCollection) error {
	return nil
}
func (m *limitMockFeeSvc) ListCollectedSummary(_ context.Context, _, _ *time.Time) ([]domain.FeeCollectionSummary, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestTransferWithinLimit(t *testing.T) {
	limit := 3
	txRepo := &limitMockTxRepo{limit: limit}
	wRepo := &limitMockWalletRepo{}
	feeSvc := &limitMockFeeSvc{}
	tenantRepo := &limitMockTenantRepo{
		tenant: &domain.Tenant{ID: "t-1", AccountType: domain.AccountTypeIndividual, MaxTransfersPerMonth: &limit},
	}

	svc := NewService(txRepo, wRepo, feeSvc, nil, tenantRepo)

	ctx := tenant.WithID(context.Background(), "t-1")

	for i := 0; i < 3; i++ {
		tx, err := svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
		if err != nil {
			t.Fatalf("transfer %d: unexpected error: %v", i, err)
		}
		if tx == nil {
			t.Fatalf("transfer %d: expected transaction", i)
		}
	}
}

func TestTransferAtExactLimit(t *testing.T) {
	limit := 2
	txRepo := &limitMockTxRepo{limit: limit}
	wRepo := &limitMockWalletRepo{}
	feeSvc := &limitMockFeeSvc{}
	tenantRepo := &limitMockTenantRepo{
		tenant: &domain.Tenant{ID: "t-1", AccountType: domain.AccountTypeIndividual, MaxTransfersPerMonth: &limit},
	}

	svc := NewService(txRepo, wRepo, feeSvc, nil, tenantRepo)

	ctx := tenant.WithID(context.Background(), "t-1")

	// First two should succeed (at limit).
	for i := 0; i < 2; i++ {
		_, err := svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
		if err != nil {
			t.Fatalf("transfer %d: unexpected error: %v", i, err)
		}
	}

	// Third should fail (exceeds limit).
	_, err := svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
	if err == nil {
		t.Fatal("expected ErrTransferLimitReached at exact limit, got nil")
	}
	if err != domain.ErrTransferLimitReached {
		t.Fatalf("expected ErrTransferLimitReached, got %v", err)
	}
}

func TestTransferExceedsLimit(t *testing.T) {
	limit := 1
	txRepo := &limitMockTxRepo{limit: limit}
	wRepo := &limitMockWalletRepo{}
	feeSvc := &limitMockFeeSvc{}
	tenantRepo := &limitMockTenantRepo{
		tenant: &domain.Tenant{ID: "t-1", AccountType: domain.AccountTypeIndividual, MaxTransfersPerMonth: &limit},
	}

	svc := NewService(txRepo, wRepo, feeSvc, nil, tenantRepo)

	ctx := tenant.WithID(context.Background(), "t-1")

	// First succeeds.
	_, err := svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second fails.
	_, err = svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
	if err != domain.ErrTransferLimitReached {
		t.Fatalf("expected ErrTransferLimitReached, got %v", err)
	}
}

func TestTransferNoLimitWhenOrg(t *testing.T) {
	// Organization accounts have unlimited transfers (-1).
	txRepo := &limitMockTxRepo{limit: 999}
	wRepo := &limitMockWalletRepo{}
	feeSvc := &limitMockFeeSvc{}
	tenantRepo := &limitMockTenantRepo{
		tenant: &domain.Tenant{ID: "t-1", AccountType: domain.AccountTypeOrganization},
	}

	svc := NewService(txRepo, wRepo, feeSvc, nil, tenantRepo)

	ctx := tenant.WithID(context.Background(), "t-1")

	for i := 0; i < 10; i++ {
		_, err := svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
		if err != nil {
			t.Fatalf("transfer %d: unexpected error: %v", i, err)
		}
	}
}

func TestConcurrentTransfersRespectLimit(t *testing.T) {
	limit := 5
	const goroutines = 20

	txRepo := &limitMockTxRepo{limit: limit}
	wRepo := &limitMockWalletRepo{}
	feeSvc := &limitMockFeeSvc{}
	tenantRepo := &limitMockTenantRepo{
		tenant: &domain.Tenant{ID: "t-1", AccountType: domain.AccountTypeIndividual, MaxTransfersPerMonth: &limit},
	}

	svc := NewService(txRepo, wRepo, feeSvc, nil, tenantRepo)

	ctx := tenant.WithID(context.Background(), "t-1")

	var wg sync.WaitGroup
	successCount := atomic.Int64{}
	errorCount := atomic.Int64{}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
			if err == nil {
				successCount.Add(1)
			} else if err == domain.ErrTransferLimitReached {
				errorCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if int(successCount.Load()) != limit {
		t.Errorf("expected exactly %d successful transfers, got %d", limit, successCount.Load())
	}
	if int(errorCount.Load()) != goroutines-limit {
		t.Errorf("expected %d rejected transfers, got %d", goroutines-limit, errorCount.Load())
	}
	// The total count in the repo must never exceed the limit.
	if int(txRepo.count.Load()) != limit {
		t.Errorf("repo count = %d, want %d", txRepo.count.Load(), limit)
	}
}

func TestConcurrentTransfersExactBoundary(t *testing.T) {
	limit := 3
	const goroutines = 10

	txRepo := &limitMockTxRepo{limit: limit}
	wRepo := &limitMockWalletRepo{}
	feeSvc := &limitMockFeeSvc{}
	tenantRepo := &limitMockTenantRepo{
		tenant: &domain.Tenant{ID: "t-1", AccountType: domain.AccountTypeIndividual, MaxTransfersPerMonth: &limit},
	}

	svc := NewService(txRepo, wRepo, feeSvc, nil, tenantRepo)

	ctx := tenant.WithID(context.Background(), "t-1")

	var wg sync.WaitGroup
	var successes, failures int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.InitiateTransfer(ctx, "w-from", "w-to", "XLM", decimal.NewFromInt(1))
			if err == nil {
				atomic.AddInt64(&successes, 1)
			} else {
				atomic.AddInt64(&failures, 1)
			}
		}()
	}

	wg.Wait()

	if successes != int64(limit) {
		t.Errorf("successes = %d, want %d", successes, limit)
	}
	if failures != int64(goroutines-limit) {
		t.Errorf("failures = %d, want %d", failures, goroutines-limit)
	}
}
