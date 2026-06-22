package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/fx"
	"github.com/jackc/pgx/v5/pgxpool"
)

// FXQuoteRepo writes FX quote audit records to Postgres.
// Redis is the live quote store; this table is the immutable audit trail.
type FXQuoteRepo struct {
	db *pgxpool.Pool
}

func NewFXQuoteRepo(db *pgxpool.Pool) *FXQuoteRepo {
	return &FXQuoteRepo{db: db}
}

// CreateQuote inserts a new quote snapshot into the audit table.
func (r *FXQuoteRepo) CreateQuote(ctx context.Context, q *fx.Quote) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO fx_quotes
		 (id, org_id, from_asset, to_asset, from_amount, to_amount, rate, fee, expires_at, used, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		q.ID, q.OrgID, q.FromAsset, q.ToAsset,
		q.FromAmount.String(), q.ToAmount.String(),
		q.Rate.String(), q.Fee.String(),
		q.ExpiresAt, false, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert fx_quote: %w", err)
	}
	return nil
}

// MarkQuoteUsed sets used = true and records the resulting conversion ID.
func (r *FXQuoteRepo) MarkQuoteUsed(ctx context.Context, quoteID, conversionID string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE fx_quotes SET used = true, conversion_id = $1 WHERE id = $2`,
		conversionID, quoteID,
	)
	if err != nil {
		return fmt.Errorf("mark fx_quote used: %w", err)
	}
	return nil
}
