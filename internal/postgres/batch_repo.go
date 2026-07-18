package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BatchRepo struct {
	db *pgxpool.Pool
}

func NewBatchRepo(db *pgxpool.Pool) *BatchRepo {
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
