package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/paymentlink"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type PaymentLinkRepo struct{ db DB }

func NewPaymentLinkRepo(db DB) *PaymentLinkRepo { return &PaymentLinkRepo{db: db} }

func (r *PaymentLinkRepo) Create(ctx context.Context, link *paymentlink.Link) error {
	created, err := scanPaymentLink(r.db.QueryRow(ctx, `
		INSERT INTO payment_links (id, token, tenant_id, wallet_id, mode, amount, currency, idempotency_key, status, expires_at, created_at)
		SELECT $1, $2, $3, w.id, $5, $6, $7, $8, $9, $10, $11
		FROM wallets w WHERE w.id = $4 AND w.tenant_id = $3 AND w.mode = $5
		ON CONFLICT (tenant_id, mode, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING id, token, tenant_id, wallet_id, mode, amount, currency, status, expires_at, created_at`,
		link.ID, link.Token, link.TenantID, link.WalletID, link.Mode,
		link.Amount.String(), link.Currency, link.IdempotencyKey, link.Status, link.ExpiresAt, link.CreatedAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, lookupErr := r.GetByIdempotencyKey(ctx, link.IdempotencyKey); lookupErr == nil {
			return paymentlink.ErrIdempotencyConflict
		}
		return domain.ErrWalletNotFound
	}
	if err != nil {
		return fmt.Errorf("create payment link: %w", err)
	}
	*link = *created
	return nil
}

func (r *PaymentLinkRepo) GetByIdempotencyKey(ctx context.Context, key string) (*paymentlink.Link, error) {
	link, err := scanPaymentLink(r.db.QueryRow(ctx, `
		SELECT id, token, tenant_id, wallet_id, mode, amount, currency, status, expires_at, created_at
		FROM payment_links WHERE tenant_id = $1 AND mode = $2 AND idempotency_key = $3`,
		tenant.IDFromContext(ctx), tenant.ModeOrDefault(ctx, domain.ModeLive), key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, paymentlink.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get payment link by idempotency key: %w", err)
	}
	return link, nil
}

func (r *PaymentLinkRepo) List(ctx context.Context) ([]*paymentlink.Link, error) {
	tenantID := tenant.IDFromContext(ctx)
	rows, err := r.db.Query(ctx, `
		SELECT id, token, tenant_id, wallet_id, mode, amount, currency, status, expires_at, created_at
		FROM payment_links WHERE tenant_id = $1 AND mode = $2 ORDER BY created_at DESC`,
		tenantID, tenant.ModeOrDefault(ctx, domain.ModeLive),
	)
	if err != nil {
		return nil, fmt.Errorf("list payment links: %w", err)
	}
	defer rows.Close()
	links := make([]*paymentlink.Link, 0)
	for rows.Next() {
		link, err := scanPaymentLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func (r *PaymentLinkRepo) Get(ctx context.Context, id string) (*paymentlink.Link, error) {
	link, err := scanPaymentLink(r.db.QueryRow(ctx, `
		SELECT id, token, tenant_id, wallet_id, mode, amount, currency, status, expires_at, created_at
		FROM payment_links WHERE id = $1 AND tenant_id = $2 AND mode = $3`,
		id, tenant.IDFromContext(ctx), tenant.ModeOrDefault(ctx, domain.ModeLive),
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, paymentlink.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get payment link: %w", err)
	}
	return link, nil
}

func (r *PaymentLinkRepo) GetPublic(ctx context.Context, token string) (*paymentlink.Link, error) {
	if _, err := uuid.Parse(token); err != nil {
		return nil, paymentlink.ErrNotFound
	}
	link, err := scanPaymentLink(r.db.QueryRow(ctx, `
		SELECT id, token, tenant_id, wallet_id, mode, amount, currency, status, expires_at, created_at
		FROM payment_links WHERE token = $1`, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, paymentlink.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get public payment link: %w", err)
	}
	return link, nil
}

func (r *PaymentLinkRepo) Claim(ctx context.Context, token string) (*paymentlink.Link, error) {
	if _, err := uuid.Parse(token); err != nil {
		return nil, paymentlink.ErrNotFound
	}
	link, err := scanPaymentLink(r.db.QueryRow(ctx, `
		UPDATE payment_links SET status = 'processing', updated_at = NOW()
		WHERE token = $1 AND status = 'active' AND expires_at > NOW()
		RETURNING id, token, tenant_id, wallet_id, mode, amount, currency, status, expires_at, created_at`, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, paymentlink.ErrNotAvailable
	}
	if err != nil {
		return nil, fmt.Errorf("claim payment link: %w", err)
	}
	return link, nil
}

func (r *PaymentLinkRepo) Release(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE payment_links SET status = 'active', updated_at = NOW()
		WHERE id = $1 AND status = 'processing' AND expires_at > NOW()`, id)
	if err != nil {
		return fmt.Errorf("release payment link: %w", err)
	}
	return nil
}

// ReleaseStale returns up to limit links that have sat in processing since
// before claimedBefore back to active, for checkouts the payer abandoned. A
// link is only released while none of its deposits has progressed past
// pending (i.e. the provider has not seen a payment); those are settled by the
// fiat webhook instead. Released links past expires_at read as expired. SKIP
// LOCKED keeps concurrent sweeps and checkouts from blocking each other.
func (r *PaymentLinkRepo) ReleaseStale(ctx context.Context, claimedBefore time.Time, limit int) (int, error) {
	tag, err := r.db.Exec(ctx, `
		UPDATE payment_links SET status = 'active', updated_at = NOW()
		WHERE id IN (
			SELECT pl.id FROM payment_links pl
			WHERE pl.status = 'processing' AND pl.updated_at <= $1
			  AND NOT EXISTS (
				SELECT 1 FROM fiat_deposits d
				WHERE d.payment_link_id = pl.id AND d.status IN ('processing', 'completed')
			  )
			ORDER BY pl.updated_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		) AND status = 'processing'`, claimedBefore, limit)
	if err != nil {
		return 0, fmt.Errorf("release stale payment links: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *PaymentLinkRepo) Cancel(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `UPDATE payment_links SET status = 'cancelled', updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND mode = $3 AND status = 'active'`,
		id, tenant.IDFromContext(ctx), tenant.ModeOrDefault(ctx, domain.ModeLive))
	if err != nil {
		return fmt.Errorf("cancel payment link: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return paymentlink.ErrNotFound
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanPaymentLink(row rowScanner) (*paymentlink.Link, error) {
	link := &paymentlink.Link{}
	var amount string
	err := row.Scan(&link.ID, &link.Token, &link.TenantID, &link.WalletID, &link.Mode,
		&amount, &link.Currency, &link.Status, &link.ExpiresAt, &link.CreatedAt)
	if err != nil {
		return nil, err
	}
	link.Amount, err = decimal.NewFromString(amount)
	if err != nil {
		return nil, fmt.Errorf("parse payment link amount: %w", err)
	}
	return link, nil
}
