package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type ScheduleRepo struct {
	db DB
}

func NewScheduleRepo(db DB) *ScheduleRepo {
	return &ScheduleRepo{db: db}
}

const scheduleColumns = `id, tenant_id, from_wallet, to_wallet, asset, amount, frequency, timezone, missed_run_policy, next_run_at, end_at, status, created_at, updated_at`

func (r *ScheduleRepo) Create(ctx context.Context, s *domain.Schedule) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		s.TenantID = &tID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO schedules (`+scheduleColumns+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		s.ID, nullableUUID(s.TenantID), s.FromWallet, s.ToWallet, s.Asset, s.Amount.String(),
		s.Frequency, s.Timezone, s.MissedRunPolicy, s.NextRunAt, nullableTime(s.EndAt), s.Status, s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert schedule: %w", err)
	}
	return nil
}

func (r *ScheduleRepo) GetByID(ctx context.Context, id string) (*domain.Schedule, error) {
	tID := tenant.IDFromContext(ctx)
	query := `SELECT ` + scheduleColumns + ` FROM schedules WHERE id = $1`
	args := []interface{}{id}
	if tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}

	sch, err := scanSchedule(r.db.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrScheduleNotFound
		}
		return nil, fmt.Errorf("get schedule by id: %w", err)
	}
	return sch, nil
}

func (r *ScheduleRepo) List(ctx context.Context) ([]*domain.Schedule, error) {
	tID := tenant.IDFromContext(ctx)
	query := `SELECT ` + scheduleColumns + ` FROM schedules`
	args := []interface{}{}
	if tID != "" {
		query += ` WHERE tenant_id = $1`
		args = append(args, tID)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()

	var schedules []*domain.Schedule
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, sch)
	}
	return schedules, rows.Err()
}

func (r *ScheduleRepo) Update(ctx context.Context, s *domain.Schedule) error {
	tID := tenant.IDFromContext(ctx)
	query := `UPDATE schedules SET amount = $2, frequency = $3, timezone = $4, missed_run_policy = $5, next_run_at = $6, end_at = $7, status = $8, updated_at = $9 WHERE id = $1`
	args := []interface{}{s.ID, s.Amount.String(), s.Frequency, s.Timezone, s.MissedRunPolicy, s.NextRunAt, nullableTime(s.EndAt), s.Status, s.UpdatedAt}
	if tID != "" {
		query += ` AND tenant_id = $10`
		args = append(args, tID)
	}

	_, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}
	return nil
}

// ListDue returns active schedules whose next_run_at has elapsed, across all
// tenants. Intended to be called with an unscoped (non-tenant) context by the
// background worker.
func (r *ScheduleRepo) ListDue(ctx context.Context, now time.Time) ([]*domain.Schedule, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+scheduleColumns+` FROM schedules WHERE status = $1 AND next_run_at <= $2`,
		domain.ScheduleStatusActive, now,
	)
	if err != nil {
		return nil, fmt.Errorf("list due schedules: %w", err)
	}
	defer rows.Close()

	var schedules []*domain.Schedule
	for rows.Next() {
		sch, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, sch)
	}
	return schedules, rows.Err()
}

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanSchedule(row rowScanner) (*domain.Schedule, error) {
	s := &domain.Schedule{}
	var amount string
	if err := row.Scan(
		&s.ID, &s.TenantID, &s.FromWallet, &s.ToWallet, &s.Asset, &amount,
		&s.Frequency, &s.Timezone, &s.MissedRunPolicy, &s.NextRunAt, &s.EndAt, &s.Status, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return nil, err
	}
	s.Amount, _ = decimal.NewFromString(amount)
	return s, nil
}

func (r *ScheduleRepo) Claim(ctx context.Context, id string, expectedNextRunAt time.Time) (bool, error) {
	query := `UPDATE schedules SET status = $1, updated_at = $2 WHERE id = $3 AND status = $4 AND next_run_at = $5`

	tag, err := r.db.Exec(ctx, query, domain.ScheduleStatusProcessing, time.Now().UTC(), id, domain.ScheduleStatusActive, expectedNextRunAt)
	if err != nil {
		return false, fmt.Errorf("claim schedule: %w", err)
	}

	return tag.RowsAffected() > 0, nil
}

// ---------------------------------------------------------------------------
// Schedule Run repository methods
// ---------------------------------------------------------------------------

const scheduleRunColumns = `id, schedule_id, tenant_id, expected_run_at, status, transaction_id, error, started_at, completed_at, created_at, updated_at`

// ClaimRun atomically inserts a pending run record for the (scheduleID,
// expectedAt) occurrence, or returns the existing record if one already
// exists.  The UNIQUE(schedule_id, expected_run_at) constraint enforces that
// only one worker can create a run for any given occurrence; concurrent
// workers that lose the race receive the already-inserted record and must
// check its status before proceeding.
func (r *ScheduleRepo) ClaimRun(ctx context.Context, scheduleID string, tenantID *string, expectedAt time.Time) (*domain.ScheduleRun, error) {
	id := uuid.New().String()
	now := time.Now().UTC()

	// Use INSERT ... ON CONFLICT DO NOTHING, then SELECT to get the winner.
	// This is a single round-trip that avoids a separate SELECT-then-INSERT
	// race while still being compatible with pgx/v5.
	_, err := r.db.Exec(ctx, `
		INSERT INTO schedule_runs
			(id, schedule_id, tenant_id, expected_run_at, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, $5)
		ON CONFLICT (schedule_id, expected_run_at) DO NOTHING`,
		id, scheduleID, nullableUUID(tenantID), expectedAt.UTC().Truncate(time.Second), now,
	)
	if err != nil {
		return nil, fmt.Errorf("claim schedule run: %w", err)
	}

	// Fetch whichever row won (ours or an earlier one).
	return r.GetRun(ctx, scheduleID, expectedAt)
}

// UpdateRun persists mutable run fields.
func (r *ScheduleRepo) UpdateRun(ctx context.Context, run *domain.ScheduleRun) error {
	run.UpdatedAt = time.Now().UTC()
	_, err := r.db.Exec(ctx, `
		UPDATE schedule_runs
		SET status         = $2,
		    transaction_id = $3,
		    error          = $4,
		    started_at     = $5,
		    completed_at   = $6,
		    updated_at     = $7
		WHERE id = $1`,
		run.ID,
		run.Status,
		nullableUUID(run.TransactionID),
		nullableStringPtr(run.Error),
		nullableTime(run.StartedAt),
		nullableTime(run.CompletedAt),
		run.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update schedule run: %w", err)
	}
	return nil
}

// GetRun returns the run record for a given (scheduleID, expectedAt) pair.
func (r *ScheduleRepo) GetRun(ctx context.Context, scheduleID string, expectedAt time.Time) (*domain.ScheduleRun, error) {
	run, err := scanScheduleRun(r.db.QueryRow(ctx,
		`SELECT `+scheduleRunColumns+` FROM schedule_runs WHERE schedule_id = $1 AND expected_run_at = $2`,
		scheduleID, expectedAt.UTC().Truncate(time.Second),
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrScheduleRunNotFound
		}
		return nil, fmt.Errorf("get schedule run: %w", err)
	}
	return run, nil
}

// ListRuns returns the run history for a schedule, tenant-scoped when a
// tenant ID is present on the context.  Results are ordered by
// expected_run_at DESC.  limit is enforced to at most 100 rows.
func (r *ScheduleRepo) ListRuns(ctx context.Context, scheduleID string, limit, offset int) ([]*domain.ScheduleRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	tID := tenant.IDFromContext(ctx)
	var args []interface{}
	query := `SELECT ` + scheduleRunColumns + ` FROM schedule_runs WHERE schedule_id = $1`
	args = append(args, scheduleID)

	if tID != "" {
		args = append(args, tID)
		query += fmt.Sprintf(` AND tenant_id = $%d`, len(args))
	}
	args = append(args, limit, offset)
	query += fmt.Sprintf(` ORDER BY expected_run_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list schedule runs: %w", err)
	}
	defer rows.Close()

	var out []*domain.ScheduleRun
	for rows.Next() {
		run, err := scanScheduleRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func scanScheduleRun(row rowScanner) (*domain.ScheduleRun, error) {
	r := &domain.ScheduleRun{}
	if err := row.Scan(
		&r.ID, &r.ScheduleID, &r.TenantID, &r.ExpectedRunAt, &r.Status,
		&r.TransactionID, &r.Error, &r.StartedAt, &r.CompletedAt,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return r, nil
}
