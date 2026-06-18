package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type APIKeyRepo struct {
	db *pgxpool.Pool
}

func NewAPIKeyRepo(db *pgxpool.Pool) *APIKeyRepo {
	return &APIKeyRepo{db: db}
}

func (r *APIKeyRepo) Create(ctx context.Context, key *domain.APIKey) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO api_keys (id, tenant_id, key_hash, prefix, label, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		key.ID, key.TenantID, key.KeyHash, key.Prefix, key.Label, key.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert api_key: %w", err)
	}
	return nil
}

func (r *APIKeyRepo) GetByHash(ctx context.Context, hash string) (*domain.APIKey, error) {
	k := &domain.APIKey{}
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, key_hash, prefix, label, last_used_at, revoked_at, created_at FROM api_keys WHERE key_hash = $1`,
		hash,
	).Scan(&k.ID, &k.TenantID, &k.KeyHash, &k.Prefix, &k.Label, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("api key not found")
		}
		return nil, fmt.Errorf("get api_key by hash: %w", err)
	}
	return k, nil
}

func (r *APIKeyRepo) ListByTenant(ctx context.Context, tenantID string) ([]*domain.APIKey, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tenant_id, key_hash, prefix, label, last_used_at, revoked_at, created_at FROM api_keys WHERE tenant_id = $1 ORDER BY created_at DESC`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list api_keys: %w", err)
	}
	defer rows.Close()

	var keys []*domain.APIKey
	for rows.Next() {
		k := &domain.APIKey{}
		if err := rows.Scan(&k.ID, &k.TenantID, &k.KeyHash, &k.Prefix, &k.Label, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (r *APIKeyRepo) Revoke(ctx context.Context, id string, tenantID string) error {
	res, err := r.db.Exec(ctx,
		`UPDATE api_keys SET revoked_at = NOW() WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	)
	if err != nil {
		return fmt.Errorf("revoke api_key: %w", err)
	}
	if res.RowsAffected() == 0 {
		return errors.New("api key not found or not owned by tenant")
	}
	return nil
}

func (r *APIKeyRepo) UpdateLastUsed(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("update api_key last_used: %w", err)
	}
	return nil
}
