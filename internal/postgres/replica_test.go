package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

// countingQuerier records whether the primary was consulted at all.
type countingQuerier struct {
	calls int
	row   pgx.Row
}

func (q *countingQuerier) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	q.calls++
	return q.row
}

type stubRow struct{ err error }

func (r stubRow) Scan(_ ...interface{}) error { return r.err }

// Regression: pgxpool.QueryRow acquires a connection that is only released by
// Scan, so building a primary row on every read and then never scanning it
// leaked one primary connection per query until the pool was exhausted and
// every subsequent request blocked. The primary must not be touched at all
// while the replica is answering.
func TestFallbackRowLeavesPrimaryUntouchedWhenReplicaSucceeds(t *testing.T) {
	primary := &countingQuerier{row: stubRow{}}
	row := &fallbackRow{
		replica:    stubRow{},
		primary:    primary,
		onFallback: func(error) { t.Fatal("fallback recorded despite a healthy replica") },
	}

	if err := row.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if primary.calls != 0 {
		t.Fatalf("primary queried %d times on the happy path, want 0", primary.calls)
	}
}

func TestFallbackRowFallsBackToPrimaryWhenReplicaFails(t *testing.T) {
	primary := &countingQuerier{row: stubRow{}}
	var recorded error
	row := &fallbackRow{
		replica:    stubRow{err: errors.New("replica unavailable")},
		primary:    primary,
		onFallback: func(err error) { recorded = err },
	}

	if err := row.Scan(); err != nil {
		t.Fatalf("Scan should have succeeded via the primary: %v", err)
	}
	if primary.calls != 1 {
		t.Fatalf("primary queried %d times, want exactly 1", primary.calls)
	}
	if recorded == nil {
		t.Fatal("replica failure was not recorded")
	}
}

func TestIsReadRoutesOnlyReads(t *testing.T) {
	reads := []string{"SELECT 1", "  select * from t", "WITH x AS (SELECT 1) SELECT * FROM x", "EXPLAIN SELECT 1"}
	writes := []string{"INSERT INTO t VALUES (1)", "UPDATE t SET a=1", "DELETE FROM t", "ALTER TABLE t ADD COLUMN a INT"}
	for _, q := range reads {
		if !isRead(q) {
			t.Fatalf("isRead(%q) = false, want true", q)
		}
	}
	for _, q := range writes {
		if isRead(q) {
			t.Fatalf("isRead(%q) = true — a write would be routed to a read replica", q)
		}
	}
}
