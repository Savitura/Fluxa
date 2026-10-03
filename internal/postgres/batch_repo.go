package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/batch"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type BatchRepo struct {
	db DB
}

func NewBatchRepo(db DB) *BatchRepo {
	return &BatchRepo{db: db}
}

func (r *BatchRepo) Create(ctx context.Context, b *domain.Batch) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		b.TenantID = &tID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO batches (id, tenant_id, status, total_count, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		b.ID, nullableUUID(b.TenantID), b.Status, b.TotalCount, b.CreatedAt, b.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert batch: %w", err)
	}
	return nil
}

func (r *BatchRepo) GetByID(ctx context.Context, id string) (*domain.Batch, error) {
	b := &domain.Batch{}
	tID := tenant.IDFromContext(ctx)

	query := `SELECT id, tenant_id, status, total_count, created_at, updated_at FROM batches WHERE id = $1`
	args := []interface{}{id}
	if tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}

	err := r.db.QueryRow(ctx, query, args...).Scan(
		&b.ID, &b.TenantID, &b.Status, &b.TotalCount, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBatchNotFound
		}
		return nil, fmt.Errorf("get batch by id: %w", err)
	}
	return b, nil
}

// List returns one page of batches for the caller's tenant, newest first.
// The keyset is (created_at DESC, id DESC) so pagination is stable even as
// new batches arrive. filter.Limit defaults to ListDefaultLimit and is capped
// at ListMaxLimit.
func (r *BatchRepo) List(ctx context.Context, filter batch.ListFilter) (*batch.ListResult, error) {
	tID := tenant.IDFromContext(ctx)
	limit := filter.Limit
	if limit <= 0 {
		limit = batch.ListDefaultLimit
	}
	if limit > batch.ListMaxLimit {
		limit = batch.ListMaxLimit
	}

	query := `SELECT id, tenant_id, status, total_count, created_at, updated_at FROM batches WHERE 1=1`
	args := []interface{}{}
	argN := 0

	if tID != "" {
		argN++
		query += fmt.Sprintf(` AND tenant_id = $%d`, argN)
		args = append(args, tID)
	}
	if filter.Status != "" {
		argN++
		query += fmt.Sprintf(` AND status = $%d`, argN)
		args = append(args, string(filter.Status))
	}
	if filter.FromWallet != "" {
		argN++
		query += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM transactions t WHERE t.batch_id = batches.id AND t.from_wallet = $%d)`, argN)
		args = append(args, filter.FromWallet)
	}
	if !filter.AfterCreated.IsZero() {
		argN++
		query += fmt.Sprintf(` AND (created_at, id) < ($%d, $%d)`, argN, argN+1)
		args = append(args, filter.AfterCreated, filter.AfterID)
		argN++
	}

	query += fmt.Sprintf(` ORDER BY created_at DESC, id DESC LIMIT $%d`, argN+1)
	args = append(args, limit+1) // fetch one extra to detect next page

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list batches: %w", err)
	}
	defer rows.Close()

	batches := make([]*domain.Batch, 0, limit+1)
	for rows.Next() {
		b := &domain.Batch{}
		if err := rows.Scan(&b.ID, &b.TenantID, &b.Status, &b.TotalCount, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan batch: %w", err)
		}
		batches = append(batches, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate batches: %w", err)
	}

	result := &batch.ListResult{Batches: batches}
	if len(batches) > limit {
		result.Batches = batches[:limit]
		last := batches[limit-1]
		result.NextCursor = &batch.ListCursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		}
	}
	return result, nil
}
