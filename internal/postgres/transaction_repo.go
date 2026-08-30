package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type TransactionRepo struct {
	db DB
}

func NewTransactionRepo(db DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) Create(ctx context.Context, tx *domain.Transaction) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		tx.TenantID = &tID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, created_at, requeue_count, reconciled_at, fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount, batch_id, reference, idempotency_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)`,
		tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
		nullableString(tx.FromWallet), nullableString(tx.ToWallet),
		tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
		nullableUUID(tx.TenantID), tx.CreatedAt,
		tx.RequeueCount, nullableTime(tx.ReconciledAt),
		nullableStringPtr(tx.FiatRail), nullableStringPtr(tx.FiatProviderRef),
		nullableStringPtr(tx.FiatStatus), nullableStringPtr(tx.LocalCurrency),
		nullableDecimalPtr(tx.LocalAmount),
		nullableUUID(tx.BatchID), nullableString(tx.Reference), nullableString(tx.IdempotencyKey),
	)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}
	return nil
}

// ExistsByTxHash reports whether a transaction with the given Stellar hash has
// already been recorded, used to keep indexer sync idempotent across restarts
// and stream reconnects.
func (r *TransactionRepo) ExistsByTxHash(ctx context.Context, txHash string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM transactions WHERE tx_hash = $1)`,
		txHash,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check transaction exists by hash: %w", err)
	}
	return exists, nil
}

func (r *TransactionRepo) GetByID(ctx context.Context, id string) (*domain.Transaction, error) {
	tx := &domain.Transaction{}
	var amount, fee string
	var localAmt *string
	var feeBps *int
	var tenantID *string
	var batchID *string
	var reference string

	tID := tenant.IDFromContext(ctx)
	query := `SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at,
		        fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
		        batch_id, COALESCE(reference,'')
		 FROM transactions WHERE id = $1`
	args := []interface{}{id}
	if tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}

	err := r.db.QueryRow(ctx, query, args...).Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
		&tx.FromWallet, &tx.ToWallet,
		&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
		&tx.RequeueCount, &tx.ReconciledAt,
		&tx.FiatRail, &tx.FiatProviderRef, &tx.FiatStatus, &tx.LocalCurrency, &localAmt,
		&batchID, &reference)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTransactionNotFound
		}
		return nil, fmt.Errorf("get transaction by id: %w", err)
	}
	tx.Amount, _ = decimal.NewFromString(amount)
	tx.Fee, _ = decimal.NewFromString(fee)
	if feeBps != nil {
		tx.FeeBps = *feeBps
	}
	tx.TenantID = tenantID
	if localAmt != nil {
		d, _ := decimal.NewFromString(*localAmt)
		tx.LocalAmount = &d
	}
	tx.BatchID = batchID
	tx.Reference = reference
	return tx, nil
}

// GetByIdempotencyKey returns the transaction previously created for this
// org/idempotency-key pair, used by the transfer service to guarantee
// exactly-once transfer creation for a given key.
func (r *TransactionRepo) GetByIdempotencyKey(ctx context.Context, orgID, idempotencyKey string) (*domain.Transaction, error) {
	tx := &domain.Transaction{}
	var amount, fee string
	var feeBps *int
	var tenantID *string
	var batchID *string
	var reference string

	query := `SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at, batch_id, COALESCE(reference,'')
		 FROM transactions WHERE idempotency_key = $1`
	args := []interface{}{idempotencyKey}
	if orgID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, orgID)
	}

	err := r.db.QueryRow(ctx, query, args...).Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
		&tx.FromWallet, &tx.ToWallet,
		&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
		&tx.RequeueCount, &tx.ReconciledAt, &batchID, &reference)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTransactionNotFound
		}
		return nil, fmt.Errorf("get transaction by idempotency key: %w", err)
	}
	tx.Amount, _ = decimal.NewFromString(amount)
	tx.Fee, _ = decimal.NewFromString(fee)
	if feeBps != nil {
		tx.FeeBps = *feeBps
	}
	tx.TenantID = tenantID
	tx.BatchID = batchID
	tx.Reference = reference
	tx.IdempotencyKey = idempotencyKey
	return tx, nil
}

// ClaimForSubmission atomically transitions a transaction from pending to
// submitted. The WHERE clause makes this a single conditional UPDATE:
// concurrent callers claiming the same id race on the same row, and only
// one can ever win it. Deliberately strict (pending only, not also
// submitted-but-unhashed): a row already sitting in `submitted` might be
// actively owned by another in-flight worker that just hasn't written its
// hash yet, so it must never be silently reclaimed here — only the
// time-gated stuck-transaction recovery path (ResetStuckSubmittedToPending,
// gated on age) may return such a row to pending.
func (r *TransactionRepo) ClaimForSubmission(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE transactions SET status = 'submitted' WHERE id = $1 AND status = 'pending'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("claim transaction for submission: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("claim transaction for submission: %w", domain.ErrConcurrentUpdate)
	}
	return nil
}

// ResetStuckSubmittedToPending recovers a transaction that was claimed
// (status=submitted) but never got a tx_hash recorded — the worker that
// claimed it crashed before reaching the network, so nothing may have been
// submitted. Gated on age (olderThan) so an in-flight worker still within
// its normal processing window is never touched; only used by the
// reconciliation sweep's stuck-transaction recovery, immediately before
// re-enqueueing. A submitted transaction that does have a hash is
// untouched by this — that one is only ever resolved by hash lookup.
func (r *TransactionRepo) ResetStuckSubmittedToPending(ctx context.Context, id string, olderThan time.Duration) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE transactions
		 SET status = 'pending'
		 WHERE id = $1
		   AND status = 'submitted'
		   AND tx_hash IS NULL
		   AND created_at < NOW() - $2::interval`,
		id, olderThan.String(),
	)
	if err != nil {
		return fmt.Errorf("reset stuck submitted transaction to pending: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("reset stuck submitted transaction to pending: %w", domain.ErrConcurrentUpdate)
	}
	return nil
}

func (r *TransactionRepo) UpdateStatus(ctx context.Context, id string, status domain.TransactionStatus, txHash string) error {
	tID := tenant.IDFromContext(ctx)
	query := `UPDATE transactions
		 SET status = $2, tx_hash = NULLIF($3, '')
		 WHERE id = $1
		   AND status != 'confirmed'`
	args := []interface{}{id, status, txHash}
	if tID != "" {
		query += ` AND tenant_id = $4`
		args = append(args, tID)
	}

	_, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update transaction status: %w", err)
	}
	return nil
}

func (r *TransactionRepo) ListByWallet(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	tID := tenant.IDFromContext(ctx)

	query := `SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at,
		        fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
		        batch_id, COALESCE(reference,'')
		 FROM transactions
		 WHERE (from_wallet = $1 OR to_wallet = $1)`
	args := []interface{}{walletID}

	if tID != "" {
		query += ` AND tenant_id = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`
		args = append(args, tID, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = append(args, limit, offset)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()

	txs, err := scanTransactions(rows)
	if err != nil {
		return nil, err
	}
	return txs, rows.Err()
}

// ListByBatch returns every transaction linked to a batch, tenant-scoped.
func (r *TransactionRepo) ListByBatch(ctx context.Context, batchID string) ([]*domain.Transaction, error) {
	tID := tenant.IDFromContext(ctx)

	query := `SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at,
		        fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
		        batch_id, COALESCE(reference,'')
		 FROM transactions WHERE batch_id = $1`
	args := []interface{}{batchID}
	if tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}
	query += ` ORDER BY created_at ASC`

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions by batch: %w", err)
	}
	defer rows.Close()

	txs, err := scanTransactions(rows)
	if err != nil {
		return nil, err
	}
	return txs, rows.Err()
}

func scanTransactions(rows pgx.Rows) ([]*domain.Transaction, error) {
	var txs []*domain.Transaction
	for rows.Next() {
		tx := &domain.Transaction{}
		var amount, fee string
		var localAmt *string
		var feeBps *int
		var tenantID, batchID *string
		var reference string
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
			&tx.FromWallet, &tx.ToWallet,
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
			&tx.RequeueCount, &tx.ReconciledAt,
			&tx.FiatRail, &tx.FiatProviderRef, &tx.FiatStatus, &tx.LocalCurrency, &localAmt,
			&batchID, &reference); err != nil {
			return nil, err
		}
		tx.Amount, _ = decimal.NewFromString(amount)
		tx.Fee, _ = decimal.NewFromString(fee)
		if feeBps != nil {
			tx.FeeBps = *feeBps
		}
		tx.TenantID = tenantID
		if localAmt != nil {
			d, _ := decimal.NewFromString(*localAmt)
			tx.LocalAmount = &d
		}
		tx.BatchID = batchID
		tx.Reference = reference
		txs = append(txs, tx)
	}
	return txs, nil
}

func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableFeeBps(bps int) interface{} {
	if bps == 0 {
		return nil
	}
	return bps
}

func nullableUUID(id *string) interface{} {
	if id == nil || *id == "" {
		return nil
	}
	return *id
}

func nullableStringPtr(s *string) interface{} {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}

func nullableDecimalPtr(d *decimal.Decimal) interface{} {
	if d == nil {
		return nil
	}
	return d.String()
}

// GetConfirmedTxesForReconciliation returns confirmed transactions with a tx_hash
// that have not been reconciled in the last hour (or never reconciled).
func (r *TransactionRepo) GetConfirmedTxesForReconciliation(ctx context.Context, since time.Duration) ([]*domain.Transaction, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tx_hash, type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at
		 FROM transactions
		 WHERE status = 'confirmed'
		   AND tx_hash IS NOT NULL
		   AND (reconciled_at IS NULL OR reconciled_at < NOW() - $1::interval)
		 ORDER BY created_at ASC`,
		since.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("get txes for reconciliation: %w", err)
	}
	defer rows.Close()

	var txs []*domain.Transaction
	for rows.Next() {
		tx := &domain.Transaction{}
		var amount, fee string
		var feeBps *int
		var tenantID *string
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
			&tx.FromWallet, &tx.ToWallet,
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
			&tx.RequeueCount, &tx.ReconciledAt); err != nil {
			return nil, err
		}
		tx.Amount, _ = decimal.NewFromString(amount)
		tx.Fee, _ = decimal.NewFromString(fee)
		if feeBps != nil {
			tx.FeeBps = *feeBps
		}
		tx.TenantID = tenantID
		txs = append(txs, tx)
	}
	return txs, rows.Err()
}

// GetStuckPendingTxes returns transactions older than the specified duration
// that never made it to the network: still pending (never claimed), or
// submitted with no tx_hash recorded (a worker claimed it but crashed
// before it could build/sign/submit). Both are safe to re-enqueue — neither
// has a hash to look up on Horizon, so nothing may have reached the network.
// A submitted transaction that does have a hash is deliberately excluded:
// that one is only ever resolved by the hash-based reconciliation path.
func (r *TransactionRepo) GetStuckPendingTxes(ctx context.Context, olderThan time.Duration) ([]*domain.Transaction, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tx_hash, type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at
		 FROM transactions
		 WHERE (status = 'pending' OR (status = 'submitted' AND tx_hash IS NULL))
		   AND created_at < NOW() - $1::interval
		 ORDER BY created_at ASC`,
		olderThan.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("get stuck pending txes: %w", err)
	}
	defer rows.Close()

	var txs []*domain.Transaction
	for rows.Next() {
		tx := &domain.Transaction{}
		var amount, fee string
		var feeBps *int
		var tenantID *string
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
			&tx.FromWallet, &tx.ToWallet,
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
			&tx.RequeueCount, &tx.ReconciledAt); err != nil {
			return nil, err
		}
		tx.Amount, _ = decimal.NewFromString(amount)
		tx.Fee, _ = decimal.NewFromString(fee)
		if feeBps != nil {
			tx.FeeBps = *feeBps
		}
		tx.TenantID = tenantID
		txs = append(txs, tx)
	}
	return txs, rows.Err()
}

// UpdateReconciliationStatus updates the status without the confirmed guard,
// allowing reconciliation to set reconciliation_failed on previously confirmed txes.
func (r *TransactionRepo) UpdateReconciliationStatus(ctx context.Context, id string, status domain.TransactionStatus) error {
	_, err := r.db.Exec(ctx,
		`UPDATE transactions SET status = $2 WHERE id = $1`,
		id, status,
	)
	if err != nil {
		return fmt.Errorf("update reconciliation status: %w", err)
	}
	return nil
}

// IncrementRequeueCount increments the requeue counter and returns the new value.
func (r *TransactionRepo) IncrementRequeueCount(ctx context.Context, id string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`UPDATE transactions SET requeue_count = requeue_count + 1 WHERE id = $1 RETURNING requeue_count`,
		id,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("increment requeue count: %w", err)
	}
	return count, nil
}

// UpdateReconciledAt sets the reconciled_at timestamp to now.
func (r *TransactionRepo) UpdateReconciledAt(ctx context.Context, id string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE transactions SET reconciled_at = NOW() WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("update reconciled_at: %w", err)
	}
	return nil
}

// WriteAuditLog inserts a row into the ledger_audit_log table.
func (r *TransactionRepo) WriteAuditLog(ctx context.Context, entry *reconcile.AuditLogEntry) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO ledger_audit_log (id, tx_id, stellar_hash, checked_at, horizon_status, amount_verified, asset_verified, fee_verified, outcome, details)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		entry.ID, entry.TxID, entry.StellarHash, entry.CheckedAt,
		entry.HorizonStatus, entry.AmountVerified, entry.AssetVerified, entry.FeeVerified,
		entry.Outcome, entry.Details,
	)
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// GetDailyReconciliationSummary returns counts grouped by day for the last 7 days.
func (r *TransactionRepo) GetDailyReconciliationSummary(ctx context.Context, days int) ([]reconcile.DailySummaryRow, error) {
	rows, err := r.db.Query(ctx,
		`SELECT d::date AS date,
		        COALESCE(SUM(CASE WHEN outcome = 'ok' THEN 1 ELSE 0 END), 0) AS ok_count,
		        COALESCE(SUM(CASE WHEN outcome = 'mismatch' THEN 1 ELSE 0 END), 0) AS mismatch_count,
		        COALESCE(SUM(CASE WHEN outcome = 'not_found' THEN 1 ELSE 0 END), 0) AS not_found_count
		 FROM generate_series(CURRENT_DATE - $1::interval, CURRENT_DATE, '1 day') d
		 LEFT JOIN ledger_audit_log ON checked_at::date = d::date
		 GROUP BY d::date
		 ORDER BY d::date DESC`,
		fmt.Sprintf("%d days", days),
	)
	if err != nil {
		return nil, fmt.Errorf("get daily reconciliation summary: %w", err)
	}
	defer rows.Close()

	var summary []reconcile.DailySummaryRow
	for rows.Next() {
		var row reconcile.DailySummaryRow
		if err := rows.Scan(&row.Date, &row.OKCount, &row.MismatchCount, &row.NotFoundCount); err != nil {
			return nil, err
		}
		summary = append(summary, row)
	}
	return summary, rows.Err()
}

// GetPendingStuckCount returns the count of transactions stuck in pending past the threshold.
func (r *TransactionRepo) GetPendingStuckCount(ctx context.Context, olderThan time.Duration) (int, error) {
	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE status = 'pending' AND created_at < NOW() - $1::interval`,
		olderThan.String(),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("get pending stuck count: %w", err)
	}
	return count, nil
}

// UpsertByTxHash inserts a transaction only if no row with the same tx_hash exists.
// Returns nil (no-op) when a duplicate is detected, making it safe for concurrent callers.
func (r *TransactionRepo) UpsertByTxHash(ctx context.Context, tx *domain.Transaction) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		tx.TenantID = &tID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, created_at, requeue_count, reconciled_at, fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount, batch_id, reference, idempotency_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22)
		 ON CONFLICT (tx_hash) DO NOTHING`,
		tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
		nullableString(tx.FromWallet), nullableString(tx.ToWallet),
		tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
		nullableUUID(tx.TenantID), tx.CreatedAt,
		tx.RequeueCount, nullableTime(tx.ReconciledAt),
		nullableStringPtr(tx.FiatRail), nullableStringPtr(tx.FiatProviderRef),
		nullableStringPtr(tx.FiatStatus), nullableStringPtr(tx.LocalCurrency),
		nullableDecimalPtr(tx.LocalAmount),
		nullableUUID(tx.BatchID), nullableString(tx.Reference), nullableString(tx.IdempotencyKey),
	)
	if err != nil {
		return fmt.Errorf("upsert transaction by tx_hash: %w", err)
	}
	return nil
}

// GetPendingTxesForReconciliation returns pending or submitted transactions
// that have a Stellar tx_hash stored and are older than olderThan —
// including a transaction whose submission outcome was ambiguous (status
// 'submitted', hash recorded, but neither confirmed nor failed yet). Uses
// SELECT FOR UPDATE SKIP LOCKED so concurrent reconciler instances claim
// disjoint sets of rows without blocking.
func (r *TransactionRepo) GetPendingTxesForReconciliation(ctx context.Context, olderThan time.Duration) ([]*domain.Transaction, error) {
	dbTx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reconciliation tx: %w", err)
	}
	defer dbTx.Rollback(ctx)

	rows, err := dbTx.Query(ctx,
		`SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at
		 FROM transactions
		 WHERE status IN ('pending', 'submitted')
		   AND tx_hash IS NOT NULL
		   AND created_at < NOW() - $1::interval
		 ORDER BY created_at ASC
		 FOR UPDATE SKIP LOCKED`,
		olderThan.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("query pending txes for reconciliation: %w", err)
	}
	defer rows.Close()

	var txs []*domain.Transaction
	for rows.Next() {
		tx := &domain.Transaction{}
		var amount, fee string
		var feeBps *int
		var tenantID *string
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
			&tx.FromWallet, &tx.ToWallet,
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
			&tx.RequeueCount, &tx.ReconciledAt); err != nil {
			return nil, err
		}
		tx.Amount, _ = decimal.NewFromString(amount)
		tx.Fee, _ = decimal.NewFromString(fee)
		if feeBps != nil {
			tx.FeeBps = *feeBps
		}
		tx.TenantID = tenantID
		txs = append(txs, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	if err := dbTx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reconciliation tx: %w", err)
	}
	return txs, nil
}

// UpdateTxConfirmed transitions a pending or submitted transaction to
// confirmed. The WHERE guard prevents double-correction if two reconcilers
// (or a reconciler and the settlement engine itself) race. Returns
// domain.ErrConcurrentUpdate when the row was already claimed.
func (r *TransactionRepo) UpdateTxConfirmed(ctx context.Context, id, txHash string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE transactions SET status = 'confirmed', tx_hash = NULLIF($2, '') WHERE id = $1 AND status IN ('pending', 'submitted')`,
		id, txHash,
	)
	if err != nil {
		return fmt.Errorf("update tx confirmed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update tx confirmed: %w", domain.ErrConcurrentUpdate)
	}
	return nil
}

// UpdateTxFailed transitions a pending or submitted transaction to failed.
// The WHERE guard prevents double-correction if two reconcilers (or a
// reconciler and the settlement engine itself) race. Returns
// domain.ErrConcurrentUpdate when the row was already claimed.
func (r *TransactionRepo) UpdateTxFailed(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE transactions SET status = 'failed' WHERE id = $1 AND status IN ('pending', 'submitted')`,
		id,
	)
	if err != nil {
		return fmt.Errorf("update tx failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update tx failed: %w", domain.ErrConcurrentUpdate)
	}
	return nil
}

// WriteReconciliationRun persists a record of a completed reconciliation pass.
func (r *TransactionRepo) WriteReconciliationRun(ctx context.Context, run *reconcile.ReconciliationRun) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO reconciliation_runs (id, started_at, completed_at, txs_checked, discrepancies_found, corrections_made)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		run.ID, run.StartedAt, run.CompletedAt, run.TxsChecked, run.DiscrepanciesFound, run.CorrectionsMade,
	)
	if err != nil {
		return fmt.Errorf("write reconciliation run: %w", err)
	}
	return nil
}

func (r *TransactionRepo) CountMonthlyTransfersByTenant(ctx context.Context, tenantID string, year int, month time.Month) (int, error) {
	startDate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`,
		tenantID, startDate, endDate,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count monthly transfers: %w", err)
	}
	return count, nil
}

// CreateWithMonthlyLimit atomically checks the tenant's monthly transfer count
// and inserts the transaction in a single database transaction. This prevents
// concurrent requests from exceeding the quota by serializing the count+insert.
func (r *TransactionRepo) CreateWithMonthlyLimit(ctx context.Context, tx *domain.Transaction, tenantID string, year int, month time.Month, limit int) error {
	dbTx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin limit-check tx: %w", err)
	}
	defer dbTx.Rollback(ctx)

	startDate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	// Serialize concurrent transfers for this tenant by locking its row first.
	// The count and the insert must be atomic or two requests can both observe
	// count == limit-1 and both insert, overshooting the quota. PostgreSQL
	// rejects FOR UPDATE on an aggregate, so the lock is taken on the tenant
	// row rather than on the counted rows; it is released on commit/rollback.
	var locked int
	err = dbTx.QueryRow(ctx, `SELECT 1 FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&locked)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lock tenant for limit check: %w", err)
	}

	var count int
	err = dbTx.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3`,
		tenantID, startDate, endDate,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("count monthly transfers: %w", err)
	}

	if count >= limit {
		return domain.ErrTransferLimitReached
	}

	_, err = dbTx.Exec(ctx,
		`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, created_at, requeue_count, reconciled_at, batch_id, reference, idempotency_key)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
		nullableString(tx.FromWallet), nullableString(tx.ToWallet),
		tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
		nullableUUID(tx.TenantID), tx.CreatedAt,
		tx.RequeueCount, nullableTime(tx.ReconciledAt),
		nullableUUID(tx.BatchID), nullableString(tx.Reference), nullableString(tx.IdempotencyKey),
	)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}

	if err := dbTx.Commit(ctx); err != nil {
		return fmt.Errorf("commit limit-check tx: %w", err)
	}
	return nil
}
