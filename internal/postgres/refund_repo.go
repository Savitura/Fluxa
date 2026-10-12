package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/refund"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

type RefundRepo struct{ db DB }

func NewRefundRepo(db DB) *RefundRepo { return &RefundRepo{db: db} }

func (r *RefundRepo) Reserve(ctx context.Context, originalID string, amount decimal.Decimal, reason, key string) (*refund.Record, error) {
	tenantID := tenant.IDFromContext(ctx)
	mode, ok := tenant.ModeFromContext(ctx)
	if tenantID == "" || !ok {
		return nil, refund.ErrNotFound
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin refund reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var originalAmount, originalFee string
	var fromWallet, toWallet, asset string
	var originalStatus domain.TransactionStatus
	var originalType domain.TransactionType
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''), asset,
		       amount::text, COALESCE(fee, 0)::text, status, type
		FROM transactions
		WHERE id = $1 AND tenant_id = $2 AND mode = $3
		FOR UPDATE`, originalID, tenantID, mode).Scan(
		&fromWallet, &toWallet, &asset, &originalAmount, &originalFee, &originalStatus, &originalType)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, refund.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock original transaction: %w", err)
	}
	if originalType != domain.TypeTransfer || (originalStatus != domain.StatusConfirmed && originalStatus != domain.StatusSettled) || fromWallet == "" || toWallet == "" {
		return nil, refund.ErrNotRefundable
	}

	if key != "" {
		record, existingErr := scanRefund(tx.QueryRow(ctx, refundSelect+`
			WHERE r.tenant_id = $1 AND r.mode = $2 AND r.idempotency_key = $3`, tenantID, mode, key))
		if existingErr == nil {
			if record.OriginalTransactionID != originalID || !record.Amount.Equal(amount) || record.Reason != reason {
				return nil, refund.ErrIdempotencyConflict
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("commit existing refund lookup: %w", err)
			}
			return record, nil
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("find existing refund: %w", existingErr)
		}
	}

	maxAmount, _ := decimal.NewFromString(originalAmount)
	fee, _ := decimal.NewFromString(originalFee)
	maxAmount = maxAmount.Sub(fee)
	if !amount.IsPositive() || amount.GreaterThan(maxAmount) {
		return nil, refund.ErrAmountExceeded
	}
	var alreadyRefunded string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)::text FROM refunds
		WHERE original_transaction_id = $1 AND tenant_id = $2 AND mode = $3
		  AND status IN ('requested', 'pending', 'succeeded')`, originalID, tenantID, mode).Scan(&alreadyRefunded); err != nil {
		return nil, fmt.Errorf("sum existing refunds: %w", err)
	}
	refunded, _ := decimal.NewFromString(alreadyRefunded)
	if refunded.Add(amount).GreaterThan(maxAmount) {
		return nil, refund.ErrAmountExceeded
	}

	created := &refund.Record{
		ID: newUUID(), OriginalTransactionID: originalID, FromWallet: toWallet,
		ToWallet: fromWallet, Asset: asset, Amount: amount, Reason: reason,
		Status: "requested", IdempotencyKey: key,
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO refunds (id, tenant_id, mode, original_transaction_id, amount, reason, status, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, 'requested', NULLIF($7, ''))`,
		created.ID, tenantID, mode, originalID, amount.String(), reason, key)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_refunds_idempotency" {
			return nil, refund.ErrIdempotencyConflict
		}
		return nil, fmt.Errorf("insert refund reservation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit refund reservation: %w", err)
	}
	return created, nil
}

func (r *RefundRepo) LinkTransaction(ctx context.Context, refundID, txID, status string) error {
	_, err := r.db.Exec(ctx, `UPDATE refunds SET refund_transaction_id = $1, status = $2, updated_at = NOW()
		WHERE id = $3 AND tenant_id = $4 AND mode = $5 AND status = 'requested'`,
		txID, statusForTransfer(status), refundID, tenant.IDFromContext(ctx), tenant.ModeOrDefault(ctx, domain.ModeLive))
	if err != nil {
		return fmt.Errorf("link refund transaction: %w", err)
	}
	return nil
}

func (r *RefundRepo) MarkFailed(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx, `UPDATE refunds SET status = 'failed', updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2 AND mode = $3 AND status = 'requested'`,
		id, tenant.IDFromContext(ctx), tenant.ModeOrDefault(ctx, domain.ModeLive))
	if err != nil {
		return fmt.Errorf("mark refund failed: %w", err)
	}
	return nil
}

func (r *RefundRepo) Get(ctx context.Context, id string) (*refund.Record, error) {
	record, err := scanRefund(r.db.QueryRow(ctx, refundSelect+`
		WHERE r.id = $1 AND r.tenant_id = $2 AND r.mode = $3`,
		id, tenant.IDFromContext(ctx), tenant.ModeOrDefault(ctx, domain.ModeLive)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, refund.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get refund: %w", err)
	}
	return record, nil
}

func (r *RefundRepo) ListByOriginal(ctx context.Context, originalID string) ([]*refund.Record, error) {
	rows, err := r.db.Query(ctx, refundSelect+`
		WHERE r.original_transaction_id = $1 AND r.tenant_id = $2 AND r.mode = $3
		ORDER BY r.created_at DESC`, originalID, tenant.IDFromContext(ctx), tenant.ModeOrDefault(ctx, domain.ModeLive))
	if err != nil {
		return nil, fmt.Errorf("list refunds: %w", err)
	}
	defer rows.Close()
	records := make([]*refund.Record, 0)
	for rows.Next() {
		record, err := scanRefund(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

const refundSelect = `SELECT r.id, r.original_transaction_id::text,
	COALESCE(r.refund_transaction_id::text,''), COALESCE(t.from_wallet::text,''),
	COALESCE(t.to_wallet::text,''), COALESCE(t.asset,''), r.amount::text, r.reason,
	CASE WHEN r.status = 'failed' OR rt.status IN ('failed', 'cancelled', 'reversed') THEN 'failed'
	     WHEN rt.status IN ('confirmed', 'settled') THEN 'succeeded'
	     WHEN r.refund_transaction_id IS NOT NULL THEN 'pending'
	     ELSE r.status END,
	COALESCE(r.idempotency_key,'')
	FROM refunds r
	JOIN transactions t ON t.id = r.original_transaction_id
	LEFT JOIN transactions rt ON rt.id = r.refund_transaction_id `

func scanRefund(row rowScanner) (*refund.Record, error) {
	record := &refund.Record{}
	var amount string
	err := row.Scan(&record.ID, &record.OriginalTransactionID, &record.RefundTransactionID,
		&record.FromWallet, &record.ToWallet, &record.Asset, &amount, &record.Reason,
		&record.Status, &record.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	record.Amount, err = decimal.NewFromString(amount)
	if err != nil {
		return nil, fmt.Errorf("parse refund amount: %w", err)
	}
	return record, nil
}

func statusForTransfer(status string) string {
	switch status {
	case string(domain.StatusConfirmed), string(domain.StatusSettled):
		return "succeeded"
	case string(domain.StatusFailed), string(domain.StatusCancelled), string(domain.StatusReversed):
		return "failed"
	default:
		return "pending"
	}
}

func newUUID() string { return uuid.NewString() }
