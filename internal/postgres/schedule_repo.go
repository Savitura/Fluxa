package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type ScheduleRepo struct {
	db *pgxpool.Pool
}

func NewScheduleRepo(db *pgxpool.Pool) *ScheduleRepo {
	return &ScheduleRepo{db: db}
}

const scheduleColumns = `id, tenant_id, from_wallet, to_wallet, asset, amount, frequency, next_run_at, end_at, status, created_at, updated_at`

func (r *ScheduleRepo) Create(ctx context.Context, s *domain.Schedule) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		s.TenantID = &tID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO schedules (`+scheduleColumns+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		s.ID, nullableUUID(s.TenantID), s.FromWallet, s.ToWallet, s.Asset, s.Amount.String(),
		s.Frequency, s.NextRunAt, nullableTime(s.EndAt), s.Status, s.CreatedAt, s.UpdatedAt,
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
	query := `UPDATE schedules SET amount = $2, frequency = $3, next_run_at = $4, end_at = $5, status = $6, updated_at = $7 WHERE id = $1`
	args := []interface{}{s.ID, s.Amount.String(), s.Frequency, s.NextRunAt, nullableTime(s.EndAt), s.Status, s.UpdatedAt}
	if tID != "" {
		query += ` AND tenant_id = $8`
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
		&s.Frequency, &s.NextRunAt, &s.EndAt, &s.Status, &s.CreatedAt, &s.UpdatedAt,
	); err != nil {
		return nil, err
	}
	s.Amount, _ = decimal.NewFromString(amount)
	return s, nil
}
