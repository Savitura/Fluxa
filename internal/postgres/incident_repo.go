package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
)

type IncidentRepository struct {
	db *DB
}

func NewIncidentRepository(db *DB) *IncidentRepository {
	return &IncidentRepository{db: db}
}

func (r *IncidentRepository) Create(ctx context.Context, inc *domain.Incident) error {
	query := `
		INSERT INTO incidents (id, title, description, severity, status, created_at, resolved_at)
		VALUES (COALESCE(NULLIF($1, ''), gen_random_uuid()), $2, $3, $4, $5, COALESCE($6, NOW()), $7)
		RETURNING id, title, description, severity, status, created_at, resolved_at
	`
	err := r.db.Pool.QueryRow(ctx, query,
		inc.ID,
		inc.Title,
		inc.Description,
		inc.Severity,
		inc.Status,
		nullTime(inc.CreatedAt),
		inc.ResolvedAt,
	).Scan(&inc.ID, &inc.Title, &inc.Description, &inc.Severity, &inc.Status, &inc.CreatedAt, &inc.ResolvedAt)
	return err
}

func (r *IncidentRepository) GetByID(ctx context.Context, id string) (*domain.Incident, error {
	query := `
		SELECT id, title, description, severity, status, created_at, resolved_at
		FROM incidents
		WHERE id = $1
	`
	inc := &domain.Incident{}
	err := r.db.Pool.QueryRow(ctx, query, id).Scan(
		&inc.ID,
		&inc.Title,
		&inc.Description,
		&inc.Severity,
		&inc.Status,
		&inc.CreatedAt,
		&inc.ResolvedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return inc, err
}

func (r *IncidentRepository) List(ctx context.Context, limit int) ([]domain.Incident, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT id, title, description, severity, status, created_at, resolved_at
		FROM incidents
		ORDER BY created_at DESC
		LIMIT $1
	`
	rows, err := r.db.Pool.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var incidents []domain.Incident
	for rows.Next() {
		var inc domain.Incident
		if err := rows.Scan(&inc.ID, &inc.Title, &inc.Description, &inc.Severity, &inc.Status, &inc.CreatedAt, &inc.ResolvedAt); err != nil {
			return nil, err
		}
		incidents = append(incidents, inc)
	}
	return incidents, nil
}

func (r *IncidentRepository) Update(ctx context.Context, inc *domain.Incident) error {
	query := `
		UPDATE incidents
		SET title = $2, description = $3, severity = $4, status = $5, resolved_at = $6
		WHERE id = $1
		RETURNING id, title, description, severity, status, created_at, resolved_at
	`
	err := r.db.Pool.QueryRow(ctx, query,
		inc.ID,
		inc.Title,
		inc.Description,
		inc.Severity,
		inc.Status,
		inc.ResolvedAt,
	).Scan(&inc.ID, &inc.Title, &inc.Description, &inc.Severity, &inc.Status, &inc.CreatedAt, &inc.ResolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}
