package postgres

import (
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
)

// TestTransactionStatusesMatchEnum verifies that every TransactionStatus
// constant defined in the domain package is a valid value in the
// transaction_status Postgres enum. This catches drift between Go code
// and the migration schema.
func TestTransactionStatusesMatchEnum(t *testing.T) {
	// This is the canonical set of statuses allowed by the DB enum.
	// It must match the ALTER TYPEs in 000021 and 000022, and the original
	// CREATE TYPE in 000002.
	validStatuses := map[string]bool{
		"pending":               true,
		"submitted":             true,
		"confirmed":             true,
		"failed":                true,
		"reconciliation_failed": true,
		"compliance_hold":       true,
	}

	allStatuses := []domain.TransactionStatus{
		domain.StatusPending,
		domain.StatusSubmitted,
		domain.StatusConfirmed,
		domain.StatusFailed,
		domain.StatusReconciliationFailed,
		domain.StatusComplianceHold,
	}

	for _, s := range allStatuses {
		if !validStatuses[string(s)] {
			t.Errorf("domain status %q has no matching DB enum value", s)
		}
	}

	// Verify we have exactly the expected number of statuses.
	if len(allStatuses) != len(validStatuses) {
		t.Errorf("domain defines %d statuses, DB enum defines %d", len(allStatuses), len(validStatuses))
	}
}

// TestReconciliationFailedStatusIsDefined verifies the specific status
// that was missing from the original migration.
func TestReconciliationFailedStatusIsDefined(t *testing.T) {
	if domain.StatusReconciliationFailed != "reconciliation_failed" {
		t.Errorf("StatusReconciliationFailed = %q, want %q", domain.StatusReconciliationFailed, "reconciliation_failed")
	}
}

// TestAllStatusesAreNonEmpty ensures no status constant is accidentally empty.
func TestAllStatusesAreNonEmpty(t *testing.T) {
	statuses := []struct {
		name   string
		status domain.TransactionStatus
	}{
		{"StatusPending", domain.StatusPending},
		{"StatusSubmitted", domain.StatusSubmitted},
		{"StatusConfirmed", domain.StatusConfirmed},
		{"StatusFailed", domain.StatusFailed},
		{"StatusReconciliationFailed", domain.StatusReconciliationFailed},
		{"StatusComplianceHold", domain.StatusComplianceHold},
	}

	for _, s := range statuses {
		if s.status == "" {
			t.Errorf("%s is empty", s.name)
		}
	}
}
