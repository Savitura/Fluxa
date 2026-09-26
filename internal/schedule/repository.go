package schedule

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// Repository manages schedules and their execution run records.
type Repository interface {
	// ---- schedule CRUD ----

	Create(ctx context.Context, s *domain.Schedule) error
	GetByID(ctx context.Context, id string) (*domain.Schedule, error)
	List(ctx context.Context) ([]*domain.Schedule, error)
	Update(ctx context.Context, s *domain.Schedule) error
	// ListDue returns active schedules whose next_run_at has elapsed. Called
	// by the background worker with an unscoped context, so it spans tenants.
	ListDue(ctx context.Context, now time.Time) ([]*domain.Schedule, error)
	Claim(ctx context.Context, id string, expectedNextRunAt time.Time) (bool, error)

	// ---- schedule run lifecycle ----

	// ClaimRun atomically inserts a run record for (scheduleID, expectedAt)
	// with status=pending, or does nothing if a record already exists.
	// It returns the current run record in both cases, allowing the caller to
	// inspect the existing state (e.g., a previously succeeded or running run)
	// and decide whether to proceed.
	//
	// The UNIQUE(schedule_id, expected_run_at) constraint in the database is
	// the authoritative guard: only one worker can win the INSERT.
	ClaimRun(ctx context.Context, scheduleID string, tenantID *string, expectedAt time.Time) (*domain.ScheduleRun, error)

	// UpdateRun persists status, transaction_id, error, started_at,
	// completed_at, and updated_at for the identified run.
	UpdateRun(ctx context.Context, run *domain.ScheduleRun) error

	// GetRun returns the run record for a given (scheduleID, expectedAt) pair.
	GetRun(ctx context.Context, scheduleID string, expectedAt time.Time) (*domain.ScheduleRun, error)

	// ListRuns returns the run history for a schedule, ordered by
	// expected_run_at DESC. The call is tenant-scoped: a non-empty tenant ID
	// on the context is injected into the WHERE clause.
	//
	// limit is capped server-side (max 100); offset enables pagination.
	ListRuns(ctx context.Context, scheduleID string, limit, offset int) ([]*domain.ScheduleRun, error)
}
