package compliance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/shopspring/decimal"
)

// complianceMockRepo is the hand-written Repository stub shared by the
// service, worker and SDN tests in this package.
type complianceMockRepo struct {
	reviews  map[string]*domain.ComplianceReview
	blocks   []*domain.ComplianceBlock
	updates  []*domain.SanctionsUpdate
	entities []*domain.SanctionsEntity

	listErr      error
	createErr    error
	decideErr    error
	replaceCalls int
}

func newComplianceMockRepo() *complianceMockRepo {
	return &complianceMockRepo{reviews: make(map[string]*domain.ComplianceReview)}
}

func (m *complianceMockRepo) CreateReview(_ context.Context, r *domain.ComplianceReview) error {
	if m.createErr != nil {
		return m.createErr
	}
	copied := *r
	m.reviews[r.ID] = &copied
	return nil
}

func (m *complianceMockRepo) GetReview(_ context.Context, id string) (*domain.ComplianceReview, error) {
	r, ok := m.reviews[id]
	if !ok {
		return nil, domain.ErrComplianceReviewNotFound
	}
	copied := *r
	return &copied, nil
}

func (m *complianceMockRepo) ListReviews(_ context.Context, status string, limit, offset int) ([]*domain.ComplianceReview, error) {
	var out []*domain.ComplianceReview
	for _, r := range m.reviews {
		if status != "" && string(r.Status) != status {
			continue
		}
		out = append(out, r)
	}
	if offset >= len(out) {
		return nil, nil
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DecideReview mirrors the production guard: only a still-pending row
// transitions, so a second concurrent decision loses.
func (m *complianceMockRepo) DecideReview(_ context.Context, id string, status domain.ReviewStatus, reviewedBy *string, notes string, decidedAt time.Time) error {
	if m.decideErr != nil {
		return m.decideErr
	}
	r, ok := m.reviews[id]
	if !ok {
		return domain.ErrComplianceReviewNotFound
	}
	if r.Status != domain.ReviewPending {
		return domain.ErrReviewNotPending
	}
	r.Status = status
	r.ReviewedBy = reviewedBy
	r.ReviewNotes = notes
	r.ReviewedAt = &decidedAt
	return nil
}

func (m *complianceMockRepo) CreateBlock(_ context.Context, b *domain.ComplianceBlock) error {
	m.blocks = append(m.blocks, b)
	return nil
}

func (m *complianceMockRepo) ReplaceSanctionsEntities(_ context.Context, entities []*domain.SanctionsEntity, _ time.Time) (int, error) {
	m.replaceCalls++
	m.entities = entities
	return len(entities), nil
}

func (m *complianceMockRepo) ListSanctionsEntities(_ context.Context) ([]*domain.SanctionsEntity, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.entities, nil
}

func (m *complianceMockRepo) RecordSanctionsUpdate(_ context.Context, u *domain.SanctionsUpdate) error {
	m.updates = append(m.updates, u)
	return nil
}

func (m *complianceMockRepo) LatestSanctionsUpdate(_ context.Context) (*domain.SanctionsUpdate, error) {
	if len(m.updates) == 0 {
		return nil, nil
	}
	return m.updates[len(m.updates)-1], nil
}

// mockTxGate stands in for the transactions table.
type mockTxGate struct {
	txs       map[string]*domain.Transaction
	updates   []statusUpdate
	updateErr error
}

type statusUpdate struct {
	id     string
	status domain.TransactionStatus
}

func newMockTxGate(txs ...*domain.Transaction) *mockTxGate {
	g := &mockTxGate{txs: make(map[string]*domain.Transaction)}
	for _, tx := range txs {
		g.txs[tx.ID] = tx
	}
	return g
}

func (g *mockTxGate) GetByID(_ context.Context, id string) (*domain.Transaction, error) {
	tx, ok := g.txs[id]
	if !ok {
		return nil, domain.ErrTransactionNotFound
	}
	return tx, nil
}

func (g *mockTxGate) UpdateStatus(_ context.Context, id string, status domain.TransactionStatus, _ string) error {
	if g.updateErr != nil {
		return g.updateErr
	}
	g.updates = append(g.updates, statusUpdate{id: id, status: status})
	if tx, ok := g.txs[id]; ok {
		tx.Status = status
	}
	return nil
}

// mockEnqueuer records every settlement enqueue.
type mockEnqueuer struct {
	enqueued []string
	err      error
}

func (q *mockEnqueuer) EnqueueTransfer(_ context.Context, txID string) error {
	if q.err != nil {
		return q.err
	}
	q.enqueued = append(q.enqueued, txID)
	return nil
}

type mockDispatcher struct {
	events []domain.EventType
}

func (d *mockDispatcher) Dispatch(_ context.Context, eventType domain.EventType, _ interface{}) error {
	d.events = append(d.events, eventType)
	return nil
}

func (d *mockDispatcher) dispatched(want domain.EventType) bool {
	for _, e := range d.events {
		if e == want {
			return true
		}
	}
	return false
}

func heldTransaction(id string) *domain.Transaction {
	return &domain.Transaction{
		ID:     id,
		Type:   domain.TypeTransfer,
		Status: domain.StatusComplianceHold,
		Asset:  "USDC",
		Amount: decimal.NewFromInt(500),
	}
}

func pendingReview(id, txID string) *domain.ComplianceReview {
	return &domain.ComplianceReview{
		ID:            id,
		TransactionID: txID,
		Status:        domain.ReviewPending,
		RiskScore:     70,
		RulesFired:    []string{"structuring"},
		CreatedAt:     time.Now().UTC(),
	}
}

// The criterion the settlement guard would silently break: approving must
// reset the row to pending BEFORE enqueuing, because
// settlement.Engine.SubmitTransfer no-ops on any other status.
func TestApproveReviewResetsStatusToPendingThenEnqueues(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	gate := newMockTxGate(heldTransaction("tx-1"))
	queue := &mockEnqueuer{}
	hooks := &mockDispatcher{}
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), gate, queue, hooks)

	review, err := svc.ApproveReview(context.Background(), "rev-1", "cleared by analyst")
	if err != nil {
		t.Fatalf("ApproveReview: %v", err)
	}
	if review.Status != domain.ReviewApproved {
		t.Fatalf("review status = %q, want approved", review.Status)
	}

	if len(gate.updates) != 1 {
		t.Fatalf("got %d status updates, want 1", len(gate.updates))
	}
	if gate.updates[0].status != domain.StatusPending {
		t.Fatalf("transfer reset to %q, want %q — settlement no-ops on anything else",
			gate.updates[0].status, domain.StatusPending)
	}
	if len(queue.enqueued) != 1 || queue.enqueued[0] != "tx-1" {
		t.Fatalf("enqueued = %v, want [tx-1]", queue.enqueued)
	}
	if !hooks.dispatched(domain.EventTransferComplianceApproved) {
		t.Fatalf("expected an approved webhook, got %v", hooks.events)
	}
}

func TestRejectReviewFailsTransferAndDoesNotEnqueue(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	gate := newMockTxGate(heldTransaction("tx-1"))
	queue := &mockEnqueuer{}
	hooks := &mockDispatcher{}
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), gate, queue, hooks)

	review, err := svc.RejectReview(context.Background(), "rev-1", "confirmed match")
	if err != nil {
		t.Fatalf("RejectReview: %v", err)
	}
	if review.Status != domain.ReviewRejected {
		t.Fatalf("review status = %q, want rejected", review.Status)
	}
	if len(gate.updates) != 1 || gate.updates[0].status != domain.StatusFailed {
		t.Fatalf("status updates = %+v, want a single transition to failed", gate.updates)
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("a rejected transfer must never be enqueued, got %v", queue.enqueued)
	}
	if !hooks.dispatched(domain.EventTransferComplianceRejected) {
		t.Fatalf("expected a rejected webhook, got %v", hooks.events)
	}
}

// Two officers clicking approve at once must release the payment once.
func TestDecidingAnAlreadyDecidedReviewIsRejected(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	gate := newMockTxGate(heldTransaction("tx-1"))
	queue := &mockEnqueuer{}
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), gate, queue, &mockDispatcher{})

	if _, err := svc.ApproveReview(context.Background(), "rev-1", ""); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	_, err := svc.ApproveReview(context.Background(), "rev-1", "")
	if !errors.Is(err, domain.ErrReviewNotPending) {
		t.Fatalf("second approve err = %v, want ErrReviewNotPending", err)
	}
	if len(queue.enqueued) != 1 {
		t.Fatalf("transfer enqueued %d times, want exactly 1", len(queue.enqueued))
	}
}

func TestRejectAfterApproveIsRejected(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	gate := newMockTxGate(heldTransaction("tx-1"))
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), gate, &mockEnqueuer{}, &mockDispatcher{})

	if _, err := svc.ApproveReview(context.Background(), "rev-1", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.RejectReview(context.Background(), "rev-1", ""); !errors.Is(err, domain.ErrReviewNotPending) {
		t.Fatalf("err = %v, want ErrReviewNotPending", err)
	}
}

func TestApproveUnknownReviewReturnsNotFound(t *testing.T) {
	svc := NewService(newComplianceMockRepo(), NewCompositeScreener(), NewSanctionsSet(),
		newMockTxGate(), &mockEnqueuer{}, &mockDispatcher{})

	_, err := svc.ApproveReview(context.Background(), "missing", "")
	if !errors.Is(err, domain.ErrComplianceReviewNotFound) {
		t.Fatalf("err = %v, want ErrComplianceReviewNotFound", err)
	}
}

func TestApproveRecordsReviewerFromContext(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(),
		newMockTxGate(heldTransaction("tx-1")), &mockEnqueuer{}, &mockDispatcher{})

	ctx := tenant.WithUser(context.Background(), "user-42", "admin")
	review, err := svc.ApproveReview(ctx, "rev-1", "")
	if err != nil {
		t.Fatalf("ApproveReview: %v", err)
	}
	if review.ReviewedBy == nil || *review.ReviewedBy != "user-42" {
		t.Fatalf("reviewed_by = %v, want user-42", review.ReviewedBy)
	}
}

// API-key auth carries no user identity; the approval must still succeed.
func TestApproveWithoutUserContextLeavesReviewerEmpty(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(),
		newMockTxGate(heldTransaction("tx-1")), &mockEnqueuer{}, &mockDispatcher{})

	review, err := svc.ApproveReview(context.Background(), "rev-1", "")
	if err != nil {
		t.Fatalf("ApproveReview: %v", err)
	}
	if review.ReviewedBy != nil {
		t.Fatalf("reviewed_by = %v, want nil under API-key auth", review.ReviewedBy)
	}
}

// A failed enqueue must not fail the approval: the row is already pending, so
// reconciliation and retries can still pick it up.
func TestApproveSurvivesEnqueueFailure(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	gate := newMockTxGate(heldTransaction("tx-1"))
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), gate,
		&mockEnqueuer{err: errors.New("redis down")}, &mockDispatcher{})

	if _, err := svc.ApproveReview(context.Background(), "rev-1", ""); err != nil {
		t.Fatalf("ApproveReview: %v", err)
	}
	if gate.updates[0].status != domain.StatusPending {
		t.Fatal("transfer should still have been reset to pending")
	}
}

// If the status reset fails, the transfer must NOT be enqueued — settlement
// would silently drop it.
func TestApproveDoesNotEnqueueWhenStatusResetFails(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.reviews["rev-1"] = pendingReview("rev-1", "tx-1")

	gate := newMockTxGate(heldTransaction("tx-1"))
	gate.updateErr = errors.New("database unreachable")
	queue := &mockEnqueuer{}
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), gate, queue, &mockDispatcher{})

	if _, err := svc.ApproveReview(context.Background(), "rev-1", ""); err == nil {
		t.Fatal("expected an error when the status reset fails")
	}
	if len(queue.enqueued) != 0 {
		t.Fatalf("enqueued %v despite the reset failing", queue.enqueued)
	}
}

func TestScreenTransferPersistsBlockRecord(t *testing.T) {
	repo := newComplianceMockRepo()
	screener := NewCompositeScreener(blockStub("sanctions", "sanctions_address_match"))
	svc := NewService(repo, screener, NewSanctionsSet(), newMockTxGate(), &mockEnqueuer{}, &mockDispatcher{})

	decision, err := svc.ScreenTransfer(context.Background(), domain.ScreeningRequest{
		OrgID:        "org-1",
		FromWalletID: "wallet-src",
		ToWalletID:   "wallet-dst",
		ToPublicKey:  sanctionedAddr,
		Asset:        "USDC",
		Amount:       decimal.NewFromInt(100),
	})
	if err != nil {
		t.Fatalf("ScreenTransfer: %v", err)
	}
	if decision.Status != domain.ScreeningBlocked {
		t.Fatalf("status = %q, want blocked", decision.Status)
	}
	if len(repo.blocks) != 1 {
		t.Fatalf("recorded %d blocks, want 1", len(repo.blocks))
	}
	if repo.blocks[0].ToAddress != sanctionedAddr {
		t.Fatalf("block recorded address %q, want %q", repo.blocks[0].ToAddress, sanctionedAddr)
	}
	if decision.BlockID == "" {
		t.Fatal("decision did not carry the persisted block id")
	}
}

// A hold writes no block row — the review is the audit trail for a hold.
func TestScreenTransferOnHoldDoesNotWriteBlock(t *testing.T) {
	repo := newComplianceMockRepo()
	svc := NewService(repo, NewCompositeScreener(holdStub("velocity", "velocity_burst")),
		NewSanctionsSet(), newMockTxGate(), &mockEnqueuer{}, &mockDispatcher{})

	decision, err := svc.ScreenTransfer(context.Background(), domain.ScreeningRequest{ToWalletID: "w"})
	if err != nil {
		t.Fatalf("ScreenTransfer: %v", err)
	}
	if decision.Status != domain.ScreeningHold {
		t.Fatalf("status = %q, want hold", decision.Status)
	}
	if len(repo.blocks) != 0 {
		t.Fatalf("a hold wrote %d block rows, want 0", len(repo.blocks))
	}
}

func TestRecordHoldCreatesReviewAndDispatchesWebhook(t *testing.T) {
	repo := newComplianceMockRepo()
	hooks := &mockDispatcher{}
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), newMockTxGate(), &mockEnqueuer{}, hooks)

	tx := heldTransaction("tx-1")
	err := svc.RecordHold(context.Background(), tx, &domain.ScreeningDecision{
		Status:     domain.ScreeningHold,
		RulesFired: []string{"structuring"},
		Reason:     "just under a round number",
		RiskScore:  70,
	})
	if err != nil {
		t.Fatalf("RecordHold: %v", err)
	}

	if len(repo.reviews) != 1 {
		t.Fatalf("created %d reviews, want 1", len(repo.reviews))
	}
	for _, r := range repo.reviews {
		if r.TransactionID != "tx-1" {
			t.Fatalf("review references %q, want tx-1", r.TransactionID)
		}
		if r.Status != domain.ReviewPending {
			t.Fatalf("new review status = %q, want pending", r.Status)
		}
		if r.RiskScore != 70 {
			t.Fatalf("risk score = %d, want 70", r.RiskScore)
		}
	}
	if !hooks.dispatched(domain.EventTransferComplianceHold) {
		t.Fatalf("expected a hold webhook, got %v", hooks.events)
	}
}

func TestRecordHoldPropagatesRepositoryFailure(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.createErr = errors.New("database unreachable")
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), newMockTxGate(), &mockEnqueuer{}, &mockDispatcher{})

	if err := svc.RecordHold(context.Background(), heldTransaction("tx-1"), &domain.ScreeningDecision{}); err == nil {
		t.Fatal("expected the review-creation failure to surface")
	}
}

func TestListReviewsClampsLimit(t *testing.T) {
	repo := newComplianceMockRepo()
	for _, id := range []string{"a", "b", "c"} {
		repo.reviews[id] = pendingReview(id, "tx-"+id)
	}
	svc := NewService(repo, NewCompositeScreener(), NewSanctionsSet(), newMockTxGate(), &mockEnqueuer{}, &mockDispatcher{})

	for _, limit := range []int{0, -5, 1000} {
		got, err := svc.ListReviews(context.Background(), "", limit, 0)
		if err != nil {
			t.Fatalf("ListReviews(limit=%d): %v", limit, err)
		}
		if len(got) > 20 {
			t.Fatalf("limit %d returned %d rows, want it clamped to 20", limit, len(got))
		}
	}
}

func TestSanctionsStatusReportsSetAndLastRefresh(t *testing.T) {
	repo := newComplianceMockRepo()
	repo.updates = append(repo.updates, &domain.SanctionsUpdate{
		Status: domain.SanctionsUpdateSuccess, EntityCount: 3, FinishedAt: time.Now().UTC(),
	})
	set := sanctionsSetFixture(t)
	svc := NewService(repo, NewCompositeScreener(), set, newMockTxGate(), &mockEnqueuer{}, &mockDispatcher{})

	status, err := svc.SanctionsStatus(context.Background())
	if err != nil {
		t.Fatalf("SanctionsStatus: %v", err)
	}
	if !status.Loaded {
		t.Fatal("status should report the set as loaded")
	}
	if status.EntityCount != 3 {
		t.Fatalf("entity count = %d, want 3", status.EntityCount)
	}
	if status.LastUpdate == nil {
		t.Fatal("status did not report the last refresh")
	}
}

func TestSanctionsStatusBeforeAnyRefresh(t *testing.T) {
	svc := NewService(newComplianceMockRepo(), NewCompositeScreener(), NewSanctionsSet(),
		newMockTxGate(), &mockEnqueuer{}, &mockDispatcher{})

	status, err := svc.SanctionsStatus(context.Background())
	if err != nil {
		t.Fatalf("SanctionsStatus: %v", err)
	}
	if status.Loaded {
		t.Fatal("an unloaded set must not report as loaded")
	}
	if status.LastUpdate != nil {
		t.Fatal("no refresh has run, so LastUpdate should be nil")
	}
}
