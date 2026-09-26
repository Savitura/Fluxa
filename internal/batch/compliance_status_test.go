package batch

import (
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
)

func txWith(status domain.TransactionStatus) *domain.Transaction {
	return &domain.Transaction{ID: "tx", Status: status}
}

// A held child must not let the batch read as completed or failed, and must
// not leave it in "processing" either — nothing will move it without a human.
func TestAggregateStatusWithComplianceHolds(t *testing.T) {
	tests := []struct {
		name string
		txs  []*domain.Transaction
		want domain.BatchStatus
	}{
		{
			name: "every child held",
			txs:  []*domain.Transaction{txWith(domain.StatusComplianceHold), txWith(domain.StatusComplianceHold)},
			want: domain.BatchStatusComplianceHold,
		},
		{
			name: "one held, the rest confirmed",
			txs:  []*domain.Transaction{txWith(domain.StatusConfirmed), txWith(domain.StatusComplianceHold)},
			want: domain.BatchStatusComplianceHold,
		},
		{
			name: "one held, the rest failed",
			txs:  []*domain.Transaction{txWith(domain.StatusFailed), txWith(domain.StatusComplianceHold)},
			want: domain.BatchStatusComplianceHold,
		},
		{
			name: "held alongside a still-settling transfer stays processing",
			txs: []*domain.Transaction{
				txWith(domain.StatusConfirmed),
				txWith(domain.StatusComplianceHold),
				txWith(domain.StatusPending),
			},
			want: domain.BatchStatusProcessing,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := aggregateStatus(tc.txs); got != tc.want {
				t.Fatalf("aggregateStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

// The pre-existing aggregation must be untouched when no child is held.
func TestAggregateStatusWithoutHoldsIsUnchanged(t *testing.T) {
	tests := []struct {
		name string
		txs  []*domain.Transaction
		want domain.BatchStatus
	}{
		{"all pending", []*domain.Transaction{txWith(domain.StatusPending)}, domain.BatchStatusPending},
		{"partially settled", []*domain.Transaction{txWith(domain.StatusConfirmed), txWith(domain.StatusPending)}, domain.BatchStatusProcessing},
		{"all confirmed", []*domain.Transaction{txWith(domain.StatusConfirmed), txWith(domain.StatusConfirmed)}, domain.BatchStatusCompleted},
		{"all failed", []*domain.Transaction{txWith(domain.StatusFailed), txWith(domain.StatusFailed)}, domain.BatchStatusFailed},
		{"mixed outcomes", []*domain.Transaction{txWith(domain.StatusConfirmed), txWith(domain.StatusFailed)}, domain.BatchStatusPartial},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := aggregateStatus(tc.txs); got != tc.want {
				t.Fatalf("aggregateStatus = %q, want %q", got, tc.want)
			}
		})
	}
}
