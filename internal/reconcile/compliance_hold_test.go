package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

// holdSkipRepo embeds mockRepo and overrides only the two methods
// RecoverPending touches before it would reach the settlement queue.
type holdSkipRepo struct {
	mockRepo
	stuck    []*domain.Transaction
	requeued []string
	failed   []string
}

func (m *holdSkipRepo) GetStuckPendingTxes(_ context.Context, _ time.Duration) ([]*domain.Transaction, error) {
	return m.stuck, nil
}

func (m *holdSkipRepo) IncrementRequeueCount(_ context.Context, id string) (int, error) {
	m.requeued = append(m.requeued, id)
	// Over maxRequeues, so the transaction takes the mark-as-failed branch and
	// never reaches the queue client, which these tests do not construct.
	return 99, nil
}

func (m *holdSkipRepo) UpdateReconciliationStatus(_ context.Context, id string, _ domain.TransactionStatus) error {
	m.failed = append(m.failed, id)
	return nil
}

func newRecoveryService(repo Repository) *Service {
	return NewService(repo, nil, nil, nil, alerting.NewClient("", "test"), nil, nil,
		"test", decimal.Zero, nil, "")
}

// A transfer held for compliance is waiting on a human, not stuck. Recovery
// must never re-enqueue one — that would release a payment compliance
// deliberately stopped.
func TestRecoverPendingSkipsComplianceHolds(t *testing.T) {
	repo := &holdSkipRepo{stuck: []*domain.Transaction{
		{ID: "tx-held", Status: domain.StatusComplianceHold, CreatedAt: time.Now().Add(-time.Hour)},
	}}

	if err := newRecoveryService(repo).RecoverPending(context.Background()); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}

	if len(repo.requeued) != 0 {
		t.Fatalf("a held transfer was put through recovery: %v", repo.requeued)
	}
	if len(repo.failed) != 0 {
		t.Fatalf("a held transfer was marked failed by recovery: %v", repo.failed)
	}
}

// The skip must be specific to held rows — genuinely stuck pending transfers
// still get recovered.
func TestRecoverPendingStillProcessesPendingTransactions(t *testing.T) {
	repo := &holdSkipRepo{stuck: []*domain.Transaction{
		{ID: "tx-held", Status: domain.StatusComplianceHold, CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "tx-stuck", Status: domain.StatusPending, CreatedAt: time.Now().Add(-time.Hour)},
	}}

	if err := newRecoveryService(repo).RecoverPending(context.Background()); err != nil {
		t.Fatalf("RecoverPending: %v", err)
	}

	if len(repo.requeued) != 1 || repo.requeued[0] != "tx-stuck" {
		t.Fatalf("recovered %v, want only [tx-stuck]", repo.requeued)
	}
	for _, id := range repo.failed {
		if id == "tx-held" {
			t.Fatal("the held transfer was marked failed")
		}
	}
}
