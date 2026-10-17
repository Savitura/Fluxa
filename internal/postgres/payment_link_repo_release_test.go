package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

// #390: the abandoned-checkout sweep must only touch links stuck in processing
// past the cutoff, and never one whose deposit the provider has already seen.
func TestReleaseStaleGuardsAndArgs(t *testing.T) {
	db := &approvalDB{}
	repo := NewPaymentLinkRepo(db)
	cutoff := time.Date(2026, 10, 17, 11, 30, 0, 0, time.UTC)

	if _, err := repo.ReleaseStale(context.Background(), cutoff, 250); err != nil {
		t.Fatalf("ReleaseStale: %v", err)
	}
	for _, guard := range []string{
		"SET status = 'active'",
		"pl.status = 'processing' AND pl.updated_at <= $1",
		"d.status IN ('processing', 'completed')",
		"FOR UPDATE SKIP LOCKED",
	} {
		if !strings.Contains(db.execSQL, guard) {
			t.Errorf("release SQL missing %q", guard)
		}
	}
	if len(db.execArgs) != 2 || db.execArgs[0] != cutoff || db.execArgs[1] != 250 {
		t.Fatalf("args = %v, want [%v 250]", db.execArgs, cutoff)
	}
}
