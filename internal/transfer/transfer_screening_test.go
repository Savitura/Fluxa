package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/shopspring/decimal"
)

// screeningMockTxRepo records every persisted transaction so tests can assert
// that a blocked transfer writes no row at all.
type screeningMockTxRepo struct {
	mu      sync.Mutex
	created []*domain.Transaction
}

func (m *screeningMockTxRepo) Create(_ context.Context, tx *domain.Transaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.created = append(m.created, tx)
	return nil
}

func (m *screeningMockTxRepo) CreateWithMonthlyLimit(_ context.Context, tx *domain.Transaction, _ string, _ int, _ time.Month, _ int) error {
	return m.Create(nil, tx)
}

// ClaimForSubmission mirrors the production guard: only a pending row can be
// claimed, so a held transfer can never be picked up for settlement.
func (m *screeningMockTxRepo) ClaimForSubmission(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tx := range m.created {
		if tx.ID == id {
			if tx.Status != domain.StatusPending {
				return domain.ErrConcurrentUpdate
			}
			tx.Status = domain.StatusSubmitted
			return nil
		}
	}
	return domain.ErrTransactionNotFound
}

func (m *screeningMockTxRepo) GetByID(_ context.Context, _ string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}
func (m *screeningMockTxRepo) UpdateStatus(_ context.Context, _ string, _ domain.TransactionStatus, _ string) error {
	return nil
}
func (m *screeningMockTxRepo) ListByWallet(_ context.Context, _ string, _, _ int) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *screeningMockTxRepo) ListByBatch(_ context.Context, _ string) ([]*domain.Transaction, error) {
	return nil, nil
}
func (m *screeningMockTxRepo) CountMonthlyTransfersByTenant(_ context.Context, _ string, _ int, _ time.Month) (int, error) {
	return 0, nil
}
func (m *screeningMockTxRepo) UpsertByTxHash(_ context.Context, _ *domain.Transaction) error {
	return nil
}
func (m *screeningMockTxRepo) ExistsByTxHash(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (m *screeningMockTxRepo) GetByIdempotencyKey(_ context.Context, _, _ string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (m *screeningMockTxRepo) createdCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.created)
}

// screeningMockScreener stands in for internal/compliance.
type screeningMockScreener struct {
	decision  *domain.ScreeningDecision
	screenErr error

	screened  []domain.ScreeningRequest
	holds     []*domain.Transaction
	recordErr error

	// byDestination lets one test give different verdicts per destination.
	byDestination map[string]*domain.ScreeningDecision
}

func (s *screeningMockScreener) ScreenTransfer(_ context.Context, req domain.ScreeningRequest) (*domain.ScreeningDecision, error) {
	s.screened = append(s.screened, req)
	if s.screenErr != nil {
		return nil, s.screenErr
	}
	if d, ok := s.byDestination[req.ToWalletID]; ok {
		return d, nil
	}
	return s.decision, nil
}

func (s *screeningMockScreener) RecordHold(_ context.Context, tx *domain.Transaction, _ *domain.ScreeningDecision) error {
	s.holds = append(s.holds, tx)
	return s.recordErr
}

func decisionOf(status domain.ScreeningStatus, rules ...string) *domain.ScreeningDecision {
	return &domain.ScreeningDecision{Status: status, RulesFired: rules, RiskScore: 70}
}

func newScreeningService(txRepo Repository, screener Screener) Service {
	// A nil queue keeps settlement out of these tests; what matters is the
	// status written to the row, which is what gates settlement.
	svc := NewService(txRepo, &limitMockWalletRepo{}, &limitMockFeeSvc{}, nil)
	if screener != nil {
		svc = svc.WithScreener(screener)
	}
	return svc
}

// Acceptance criterion: a transfer to a sanctioned address is refused and is
// never persisted, so nothing can later pick it up and submit it.
func TestBlockedTransferIsRefusedAndNeverPersisted(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	screener := &screeningMockScreener{decision: decisionOf(domain.ScreeningBlocked, "sanctions_address_match")}
	svc := newScreeningService(txRepo, screener)

	tx, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(100))

	if !errors.Is(err, domain.ErrTransferBlockedSanctions) {
		t.Fatalf("err = %v, want ErrTransferBlockedSanctions", err)
	}
	if tx != nil {
		t.Fatalf("a blocked transfer returned a transaction: %+v", tx)
	}
	if txRepo.createdCount() != 0 {
		t.Fatalf("a blocked transfer persisted %d rows, want 0", txRepo.createdCount())
	}
	if len(screener.holds) != 0 {
		t.Fatalf("a blocked transfer recorded %d holds, want 0", len(screener.holds))
	}
}

// The blocked sentinel must map to 403 with its own code, not the generic
// FORBIDDEN and not a 500.
func TestBlockedTransferMapsTo403WithSanctionsCode(t *testing.T) {
	rec := httptest.NewRecorder()
	api.HandleDomainError(rec, domain.ErrTransferBlockedSanctions)

	if rec.Code != 403 {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error.Code != "TRANSFER_BLOCKED_SANCTIONS" {
		t.Fatalf("code = %q, want TRANSFER_BLOCKED_SANCTIONS", body.Error.Code)
	}
}

// A held transfer is persisted so it can be reviewed, but with a status that
// settlement refuses to act on.
func TestHeldTransferIsPersistedAsComplianceHoldAndNotEnqueued(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	screener := &screeningMockScreener{decision: decisionOf(domain.ScreeningHold, "structuring")}
	svc := newScreeningService(txRepo, screener)

	tx, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(999))
	if err != nil {
		t.Fatalf("InitiateTransfer: %v", err)
	}
	if tx.Status != domain.StatusComplianceHold {
		t.Fatalf("status = %q, want %q", tx.Status, domain.StatusComplianceHold)
	}
	if txRepo.createdCount() != 1 {
		t.Fatalf("persisted %d rows, want 1", txRepo.createdCount())
	}
	if len(screener.holds) != 1 || screener.holds[0].ID != tx.ID {
		t.Fatalf("expected exactly one recorded hold for %s, got %+v", tx.ID, screener.holds)
	}
}

// settlement.Engine.SubmitTransfer no-ops on any status but pending, so a
// held row must never be written as pending.
func TestHeldTransferStatusIsNotPending(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	svc := newScreeningService(txRepo, &screeningMockScreener{decision: decisionOf(domain.ScreeningHold, "velocity_burst")})

	tx, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(1))
	if err != nil {
		t.Fatalf("InitiateTransfer: %v", err)
	}
	if tx.Status == domain.StatusPending {
		t.Fatal("held transfer was written as pending and would be settled immediately")
	}
}

func TestClearedTransferIsPending(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	svc := newScreeningService(txRepo, &screeningMockScreener{decision: decisionOf(domain.ScreeningClear)})

	tx, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(10))
	if err != nil {
		t.Fatalf("InitiateTransfer: %v", err)
	}
	if tx.Status != domain.StatusPending {
		t.Fatalf("status = %q, want pending", tx.Status)
	}
}

// Fail closed at the transfer boundary too: a screener that errors must not
// let the payment through.
func TestScreenerErrorHoldsRatherThanClears(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	screener := &screeningMockScreener{screenErr: errors.New("database unreachable")}
	svc := newScreeningService(txRepo, screener)

	tx, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(10))
	if err != nil {
		t.Fatalf("a screener failure should hold, not error the request: %v", err)
	}
	if tx.Status != domain.StatusComplianceHold {
		t.Fatalf("status = %q, want compliance_hold when screening fails", tx.Status)
	}
}

// Acceptance criterion: holding one transfer must not stop the org's others.
func TestHoldDoesNotBlockOtherTransfersForTheSameOrg(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	screener := &screeningMockScreener{
		decision: decisionOf(domain.ScreeningClear),
		byDestination: map[string]*domain.ScreeningDecision{
			"wallet-suspicious": decisionOf(domain.ScreeningHold, "structuring"),
		},
	}
	svc := newScreeningService(txRepo, screener)
	ctx := tenant.WithID(context.Background(), "org-1")

	held, err := svc.InitiateTransfer(ctx, "wallet-a", "wallet-suspicious", "XLM", decimal.NewFromInt(999))
	if err != nil {
		t.Fatalf("first transfer: %v", err)
	}
	if held.Status != domain.StatusComplianceHold {
		t.Fatalf("first transfer status = %q, want compliance_hold", held.Status)
	}

	clear, err := svc.InitiateTransfer(ctx, "wallet-a", "wallet-ordinary", "XLM", decimal.NewFromInt(25))
	if err != nil {
		t.Fatalf("second transfer for the same org was refused: %v", err)
	}
	if clear.Status != domain.StatusPending {
		t.Fatalf("second transfer status = %q, want pending — a hold must not be org-wide", clear.Status)
	}
}

// Screening lives in initiate(), so batch and scheduled transfers are covered
// by the same call without a second integration point.
func TestBatchTransfersAreScreenedToo(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	screener := &screeningMockScreener{decision: decisionOf(domain.ScreeningBlocked, "sanctions_address_match")}
	svc := newScreeningService(txRepo, screener)

	_, err := svc.InitiateBatchTransfer(context.Background(), "wallet-a", "wallet-b", "XLM",
		decimal.NewFromInt(50), "batch-1", "ref-1")
	if !errors.Is(err, domain.ErrTransferBlockedSanctions) {
		t.Fatalf("batch transfer err = %v, want ErrTransferBlockedSanctions", err)
	}
	if len(screener.screened) != 1 {
		t.Fatalf("batch transfer was screened %d times, want 1", len(screener.screened))
	}
}

func TestIdempotentTransfersAreScreenedToo(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	screener := &screeningMockScreener{decision: decisionOf(domain.ScreeningBlocked, "sanctions_address_match")}
	svc := newScreeningService(txRepo, screener)

	_, err := svc.InitiateTransferIdempotent(context.Background(), "wallet-a", "wallet-b", "XLM",
		decimal.NewFromInt(50), "9f1d0c6e-0000-4000-8000-000000000000")
	if !errors.Is(err, domain.ErrTransferBlockedSanctions) {
		t.Fatalf("err = %v, want ErrTransferBlockedSanctions", err)
	}
}

// Screening must receive the real wallet identifiers and public keys, not
// placeholders — the sanctions rule matches on the destination public key.
func TestScreeningRequestCarriesWalletDetails(t *testing.T) {
	screener := &screeningMockScreener{decision: decisionOf(domain.ScreeningClear)}
	svc := newScreeningService(&screeningMockTxRepo{}, screener)
	ctx := tenant.WithID(context.Background(), "org-7")

	if _, err := svc.InitiateTransfer(ctx, "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(42)); err != nil {
		t.Fatalf("InitiateTransfer: %v", err)
	}

	if len(screener.screened) != 1 {
		t.Fatalf("screened %d times, want 1", len(screener.screened))
	}
	req := screener.screened[0]
	if req.OrgID != "org-7" {
		t.Fatalf("OrgID = %q, want org-7", req.OrgID)
	}
	if req.FromWalletID != "wallet-a" || req.ToWalletID != "wallet-b" {
		t.Fatalf("wallet ids = %q/%q, want wallet-a/wallet-b", req.FromWalletID, req.ToWalletID)
	}
	if req.ToPublicKey != "Gwallet-b" {
		t.Fatalf("ToPublicKey = %q, want the destination wallet's key", req.ToPublicKey)
	}
	if !req.Amount.Equal(decimal.NewFromInt(42)) {
		t.Fatalf("Amount = %s, want 42", req.Amount)
	}
}

// Without a screener the service must behave exactly as it did before
// compliance existed — this is what keeps the worker's wiring valid.
func TestTransfersAreUnscreenedWhenNoScreenerIsAttached(t *testing.T) {
	txRepo := &screeningMockTxRepo{}
	svc := newScreeningService(txRepo, nil)

	tx, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(10))
	if err != nil {
		t.Fatalf("InitiateTransfer: %v", err)
	}
	if tx.Status != domain.StatusPending {
		t.Fatalf("status = %q, want pending", tx.Status)
	}
}

// A failure to write the review row must fail the request rather than leave a
// held transfer with no review attached to it.
func TestHoldFailsWhenTheReviewCannotBeRecorded(t *testing.T) {
	screener := &screeningMockScreener{
		decision:  decisionOf(domain.ScreeningHold, "structuring"),
		recordErr: errors.New("database unreachable"),
	}
	svc := newScreeningService(&screeningMockTxRepo{}, screener)

	if _, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-b", "XLM", decimal.NewFromInt(1)); err == nil {
		t.Fatal("expected an error when the compliance review cannot be recorded")
	}
}

// Screening runs after the self-transfer guard, so an obviously invalid
// request is still rejected on its own terms.
func TestSelfTransferIsRejectedBeforeScreening(t *testing.T) {
	screener := &screeningMockScreener{decision: decisionOf(domain.ScreeningClear)}
	svc := newScreeningService(&screeningMockTxRepo{}, screener)

	if _, err := svc.InitiateTransfer(context.Background(), "wallet-a", "wallet-a", "XLM", decimal.NewFromInt(1)); !errors.Is(err, domain.ErrSelfTransfer) {
		t.Fatalf("err = %v, want ErrSelfTransfer", err)
	}
	if len(screener.screened) != 0 {
		t.Fatal("a self-transfer should not reach the screener")
	}
}
