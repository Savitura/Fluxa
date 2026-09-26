package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type ComplianceRepo struct {
	db DB
	// primary is used for the reads that back a compliance decision. The
	// screening queries count recent transfers inside windows as short as ten
	// minutes, and a burst of transfers is exactly what the velocity rule
	// exists to catch — reading those counts from a replica that is seconds
	// behind (entirely possible across regions) would undercount the burst and
	// silently weaken the control. Bulk sanctions-list reads stay on db, where
	// replica lag is harmless and read-scaling is the point.
	primary DB
}

func NewComplianceRepo(db DB) *ComplianceRepo {
	return &ComplianceRepo{db: db, primary: db}
}

// WithPrimary routes compliance-decision reads at the primary, bypassing any
// read replica. Without it the repo falls back to its main handle, so a
// single-region deployment needs no extra wiring.
func (r *ComplianceRepo) WithPrimary(primary DB) *ComplianceRepo {
	if primary != nil {
		r.primary = primary
	}
	return r
}

const reviewColumns = `id, transaction_id, org_id, status, risk_score, rules_fired, reason,
	reviewed_by, review_notes, reviewed_at, created_at`

func scanReview(row pgx.Row) (*domain.ComplianceReview, error) {
	r := &domain.ComplianceReview{}
	err := row.Scan(&r.ID, &r.TransactionID, &r.OrgID, &r.Status, &r.RiskScore,
		&r.RulesFired, &r.Reason, &r.ReviewedBy, &r.ReviewNotes, &r.ReviewedAt, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ComplianceRepo) CreateReview(ctx context.Context, review *domain.ComplianceReview) error {
	if tID := tenant.IDFromContext(ctx); tID != "" && review.OrgID == nil {
		review.OrgID = &tID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO compliance_reviews (id, transaction_id, org_id, status, risk_score, rules_fired, reason, review_notes, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		review.ID, review.TransactionID, nullableUUID(review.OrgID), review.Status,
		review.RiskScore, review.RulesFired, review.Reason, review.ReviewNotes, review.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert compliance review: %w", err)
	}
	return nil
}

func (r *ComplianceRepo) GetReview(ctx context.Context, id string) (*domain.ComplianceReview, error) {
	query := `SELECT ` + reviewColumns + ` FROM compliance_reviews WHERE id = $1`
	args := []interface{}{id}
	if tID := tenant.IDFromContext(ctx); tID != "" {
		query += ` AND org_id = $2`
		args = append(args, tID)
	}

	review, err := scanReview(r.primary.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrComplianceReviewNotFound
		}
		return nil, fmt.Errorf("get compliance review: %w", err)
	}
	return review, nil
}

func (r *ComplianceRepo) ListReviews(ctx context.Context, status string, limit, offset int) ([]*domain.ComplianceReview, error) {
	query := `SELECT ` + reviewColumns + ` FROM compliance_reviews WHERE 1=1`
	args := []interface{}{}

	if tID := tenant.IDFromContext(ctx); tID != "" {
		args = append(args, tID)
		query += fmt.Sprintf(` AND org_id = $%d`, len(args))
	}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(` AND status = $%d`, len(args))
	}

	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))
	args = append(args, offset)
	query += fmt.Sprintf(` OFFSET $%d`, len(args))

	rows, err := r.primary.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list compliance reviews: %w", err)
	}
	defer rows.Close()

	var reviews []*domain.ComplianceReview
	for rows.Next() {
		review, err := scanReview(rows)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, review)
	}
	return reviews, rows.Err()
}

// DecideReview transitions a review out of pending. The `status = 'pending'`
// predicate is the concurrency guard: two simultaneous approvals race here,
// and the loser sees zero rows affected and gets ErrReviewNotPending rather
// than both going on to enqueue the same transfer.
func (r *ComplianceRepo) DecideReview(ctx context.Context, id string, status domain.ReviewStatus, reviewedBy *string, notes string, decidedAt time.Time) error {
	query := `UPDATE compliance_reviews
		 SET status = $2, reviewed_by = $3, review_notes = $4, reviewed_at = $5
		 WHERE id = $1 AND status = 'pending'`
	args := []interface{}{id, status, nullableUUID(reviewedBy), notes, decidedAt}
	if tID := tenant.IDFromContext(ctx); tID != "" {
		query += ` AND org_id = $6`
		args = append(args, tID)
	}

	tag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("decide compliance review: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrReviewNotPending
	}
	return nil
}

func (r *ComplianceRepo) CreateBlock(ctx context.Context, b *domain.ComplianceBlock) error {
	if tID := tenant.IDFromContext(ctx); tID != "" && b.OrgID == nil {
		b.OrgID = &tID
	}
	var amount interface{}
	if b.Amount != nil {
		amount = b.Amount.String()
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO compliance_blocks (id, org_id, from_wallet_id, to_wallet_id, to_address, asset, amount, rules_fired, reason, matched_entity, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		b.ID, nullableUUID(b.OrgID), nullableUUID(b.FromWalletID), nullableUUID(b.ToWalletID),
		b.ToAddress, b.Asset, amount, b.RulesFired, b.Reason, b.MatchedEntity, b.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert compliance block: %w", err)
	}
	return nil
}

// ReplaceSanctionsEntities upserts the refreshed list and deletes rows that
// the new list no longer contains, in one transaction — a delisted entity must
// not linger and keep blocking payments.
func (r *ComplianceRepo) ReplaceSanctionsEntities(ctx context.Context, entities []*domain.SanctionsEntity, refreshedAt time.Time) (int, error) {
	if len(entities) == 0 {
		return 0, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin sanctions refresh: %w", err)
	}
	defer tx.Rollback(ctx)

	batch := &pgx.Batch{}
	for _, e := range entities {
		batch.Queue(
			`INSERT INTO sanctions_entities (uid, name, entity_type, address, address_type, programs, source, refreshed_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (uid, name, address) DO UPDATE SET
			   entity_type = EXCLUDED.entity_type,
			   address_type = EXCLUDED.address_type,
			   programs = EXCLUDED.programs,
			   source = EXCLUDED.source,
			   refreshed_at = EXCLUDED.refreshed_at`,
			e.UID, e.Name, e.EntityType, e.Address, e.AddressType, e.Programs, e.Source, refreshedAt,
		)
	}

	results := tx.SendBatch(ctx, batch)
	for range entities {
		if _, err := results.Exec(); err != nil {
			results.Close()
			return 0, fmt.Errorf("upsert sanctions entity: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return 0, fmt.Errorf("close sanctions batch: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM sanctions_entities WHERE source = $1 AND refreshed_at < $2`,
		entities[0].Source, refreshedAt,
	); err != nil {
		return 0, fmt.Errorf("prune stale sanctions entities: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit sanctions refresh: %w", err)
	}
	return len(entities), nil
}

// ListSanctionsEntities is intentionally not tenant-scoped: the sanctions list
// is platform-wide reference data, not tenant-owned rows.
func (r *ComplianceRepo) ListSanctionsEntities(ctx context.Context) ([]*domain.SanctionsEntity, error) {
	rows, err := r.db.Query(ctx,
		`SELECT uid, name, entity_type, address, address_type, programs, source, refreshed_at
		 FROM sanctions_entities`)
	if err != nil {
		return nil, fmt.Errorf("list sanctions entities: %w", err)
	}
	defer rows.Close()

	var entities []*domain.SanctionsEntity
	for rows.Next() {
		e := &domain.SanctionsEntity{}
		if err := rows.Scan(&e.UID, &e.Name, &e.EntityType, &e.Address, &e.AddressType,
			&e.Programs, &e.Source, &e.RefreshedAt); err != nil {
			return nil, err
		}
		entities = append(entities, e)
	}
	return entities, rows.Err()
}

func (r *ComplianceRepo) RecordSanctionsUpdate(ctx context.Context, u *domain.SanctionsUpdate) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO sanctions_list_updates (id, source, status, entity_count, duration_ms, error, started_at, finished_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		u.ID, u.Source, u.Status, u.EntityCount, u.DurationMS, u.Error, u.StartedAt, u.FinishedAt,
	)
	if err != nil {
		return fmt.Errorf("record sanctions update: %w", err)
	}
	return nil
}

func (r *ComplianceRepo) LatestSanctionsUpdate(ctx context.Context) (*domain.SanctionsUpdate, error) {
	u := &domain.SanctionsUpdate{}
	err := r.db.QueryRow(ctx,
		`SELECT id, source, status, entity_count, duration_ms, error, started_at, finished_at
		 FROM sanctions_list_updates ORDER BY finished_at DESC LIMIT 1`,
	).Scan(&u.ID, &u.Source, &u.Status, &u.EntityCount, &u.DurationMS, &u.Error, &u.StartedAt, &u.FinishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No refresh has run yet — not an error, the status endpoint
			// reports it as "never refreshed".
			return nil, nil
		}
		return nil, fmt.Errorf("latest sanctions update: %w", err)
	}
	return u, nil
}

// CountTransfersByOrgSince backs the velocity rule.
func (r *ComplianceRepo) CountTransfersByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error) {
	var count int
	err := r.primary.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions
		 WHERE tenant_id = $1 AND type = 'transfer' AND created_at >= $2`,
		orgID, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count transfers by org: %w", err)
	}
	return count, nil
}

// AggregateTransfersToDestinationSince backs the structuring rule.
func (r *ComplianceRepo) AggregateTransfersToDestinationSince(ctx context.Context, orgID, toWalletID string, since time.Time) (int, decimal.Decimal, error) {
	query := `SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM transactions
		 WHERE to_wallet = $1 AND type = 'transfer' AND created_at >= $2`
	args := []interface{}{toWalletID, since}
	if orgID != "" {
		args = append(args, orgID)
		query += fmt.Sprintf(` AND tenant_id = $%d`, len(args))
	}

	var count int
	var sum string
	if err := r.primary.QueryRow(ctx, query, args...).Scan(&count, &sum); err != nil {
		return 0, decimal.Zero, fmt.Errorf("aggregate transfers to destination: %w", err)
	}
	total, err := decimal.NewFromString(sum)
	if err != nil {
		return 0, decimal.Zero, fmt.Errorf("parse destination transfer sum %q: %w", sum, err)
	}
	return count, total, nil
}

// HasInboundTransferSince backs the round-trip rule.
func (r *ComplianceRepo) HasInboundTransferSince(ctx context.Context, walletID, counterpartyWalletID string, since time.Time) (bool, error) {
	var exists bool
	err := r.primary.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM transactions
		   WHERE to_wallet = $1 AND from_wallet = $2 AND created_at >= $3
		     AND status IN ('pending', 'submitted', 'confirmed')
		 )`,
		walletID, counterpartyWalletID, since,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check inbound transfer: %w", err)
	}
	return exists, nil
}
