package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TenantRepo struct {
	db *pgxpool.Pool
}

func NewTenantRepo(db *pgxpool.Pool) *TenantRepo {
	return &TenantRepo{db: db}
}

func (r *TenantRepo) Create(ctx context.Context, t *domain.Tenant) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO tenants (id, name, email, created_at)
		 VALUES ($1, $2, $3, $4)`,
		t.ID, t.Name, t.Email, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert tenant: %w", err)
	}
	return nil
}

func (r *TenantRepo) GetByID(ctx context.Context, id string) (*domain.Tenant, error) {
	t := &domain.Tenant{}
	err := r.db.QueryRow(ctx,
		`SELECT id, name, email, created_at FROM tenants WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.Name, &t.Email, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("tenant not found")
		}
		return nil, fmt.Errorf("get tenant by id: %w", err)
	}
	return t, nil
}
