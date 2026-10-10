package domain

import "testing"

func TestReconciliationDiscrepancyCategory(t *testing.T) {
	tests := []struct {
		name  string
		entry AuditLogEntry
		want  string
	}{
		{"not found", AuditLogEntry{Outcome: AuditNotFound}, DiscrepancyMissingOnChain},
		{"duplicate", AuditLogEntry{Outcome: AuditMismatch, Details: "duplicate settlement operation"}, DiscrepancyDuplicateSettlement},
		{"stale pending", AuditLogEntry{Outcome: AuditMismatch, HorizonStatus: "pending"}, DiscrepancyStalePending},
		{"asset", AuditLogEntry{Outcome: AuditMismatch, AssetVerified: false}, DiscrepancyAssetMismatch},
		{"amount", AuditLogEntry{Outcome: AuditMismatch, AssetVerified: true, AmountVerified: false}, DiscrepancyAmountMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReconciliationDiscrepancyCategory(&tt.entry); got != tt.want {
				t.Fatalf("category = %q, want %q", got, tt.want)
			}
		})
	}
}
