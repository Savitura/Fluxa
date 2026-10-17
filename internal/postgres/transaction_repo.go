package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

type TransactionRepo struct {
	db      DB
	primary DB
}

func NewTransactionRepo(db DB) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) WithPrimary(primary DB) *TransactionRepo {
	r.primary = primary
	return r
}

func (r *TransactionRepo) readDB() DB {
	if r.primary != nil {
		return r.primary
	}
	return r.db
}

func mapTransactionInsertError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		(pgErr.ConstraintName == "uq_transactions_idempotency_record" || pgErr.ConstraintName == "uq_transactions_mode_tx_hash") {
		return fmt.Errorf("%w: %v", domain.ErrConcurrentUpdate, err)
	}
	return err
}

func transactionMode(ctx context.Context) domain.Mode {
	return tenant.ModeOrDefault(ctx, domain.ModeLive)
}

func (r *TransactionRepo) Create(ctx context.Context, tx *domain.Transaction) error {
	if tx.Mode == "" {
		tx.Mode = transactionMode(ctx)
	}
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		tx.TenantID = &tID
	}
	if tx.Tags == nil {
		tx.Tags = []string{}
	}
	_, err := TxFromContext(ctx, r.db).Exec(ctx,
		`INSERT INTO transactions (
			id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps,
			tenant_id, mode, created_at, requeue_count, reconciled_at,
			fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
			batch_id, reference, external_reference, tags, idempotency_key, failure_reason, failure_message
		 ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27)`,
		tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
		nullableString(tx.FromWallet), nullableString(tx.ToWallet),
		tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
		nullableUUID(tx.TenantID), tx.Mode, tx.CreatedAt,
		tx.RequeueCount, nullableTime(tx.ReconciledAt),
		nullableStringPtr(tx.FiatRail), nullableStringPtr(tx.FiatProviderRef),
		nullableStringPtr(tx.FiatStatus), nullableStringPtr(tx.LocalCurrency),
		nullableDecimalPtr(tx.LocalAmount),
		nullableUUID(tx.BatchID), nullableString(tx.Reference),
		nullableStringPtr(tx.ExternalReference), tx.Tags,
		nullableString(tx.IdempotencyKey),
		nullableString(tx.FailureReason), nullableString(tx.FailureMessage),
	)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", mapTransactionInsertError(err))
	}
	return nil
}

// ExistsByTxHash reports whether a transaction with the given Stellar hash has
// already been recorded, used to keep indexer sync idempotent across restarts
// and stream reconnects.
func (r *TransactionRepo) ExistsByTxHash(ctx context.Context, txHash string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM transactions WHERE tx_hash = $1 AND mode = $2)`,
		txHash, transactionMode(ctx),
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
	var idempotencyRecordID *string
	var reference string
	var externalRef *string
	var tags []string
	var failureReason, failureMessage string

	mode := transactionMode(ctx)
	query := `SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, mode, created_at,
		        COALESCE(requeue_count, 0), reconciled_at,
		        fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
		        batch_id, COALESCE(reference,''), external_reference, COALESCE(tags, '{}'),
		        COALESCE(failure_reason,''), COALESCE(failure_message,''), idempotency_record_id
		 FROM transactions WHERE id = $1 AND mode = $2`
	args := []interface{}{id, mode}

	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		query += ` AND tenant_id = $3`
		args = append(args, tID)
	}

	err := r.readDB().QueryRow(ctx, query, args...).Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
		&tx.FromWallet, &tx.ToWallet,
		&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.Mode, &tx.CreatedAt,
		&tx.RequeueCount, &tx.ReconciledAt,
		&tx.FiatRail, &tx.FiatProviderRef, &tx.FiatStatus, &tx.LocalCurrency, &localAmt,
		&batchID, &reference, &externalRef, &tags, &failureReason, &failureMessage, &idempotencyRecordID)
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
	tx.IdempotencyRecordID = idempotencyRecordID
	if localAmt != nil {
		d, _ := decimal.NewFromString(*localAmt)
		tx.LocalAmount = &d
	}
	tx.BatchID = batchID
	tx.Reference = reference
	tx.ExternalReference = externalRef
	tx.Tags = tags
	tx.FailureReason = failureReason
	tx.FailureMessage = failureMessage
	return tx, nil
}

// GetByIdempotencyKey returns the transaction previously created for this
// tenant/mode/idempotency-key tuple.
func (r *TransactionRepo) GetByIdempotencyKey(ctx context.Context, orgID, idempotencyKey string) (*domain.Transaction, error) {
	mode := transactionMode(ctx)
	tx, err := r.getIdempotentTransaction(ctx, "idempotency_key = $1", idempotencyKey, mode, orgID)
	if tx != nil {
		tx.IdempotencyKey = idempotencyKey
	}
	return tx, err
}

// GetByIdempotencyRecordID returns the transaction linked to the exact
// idempotency generation. This is the recovery fence used after a process dies
// after inserting a transfer but before completing the idempotency record.
func (r *TransactionRepo) GetByIdempotencyRecordID(ctx context.Context, recordID string) (*domain.Transaction, error) {
	return r.getIdempotentTransaction(ctx, "idempotency_record_id = $1", recordID, transactionMode(ctx), tenant.IDFromContext(ctx))
}

func (r *TransactionRepo) getIdempotentTransaction(ctx context.Context, predicate, value string, mode domain.Mode, orgID string) (*domain.Transaction, error) {
	tx := &domain.Transaction{}
	var amount, fee string
	var feeBps *int
	var tenantID, batchID, recordID *string
	var reference string
	var externalRef *string
	var tags []string
	var failureReason, failureMessage, idempotencyKey string
	query := `SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, mode, created_at,
		        COALESCE(requeue_count, 0), reconciled_at, batch_id, COALESCE(reference,''),
		        external_reference, COALESCE(tags, '{}'),
		        COALESCE(failure_reason,''), COALESCE(failure_message,''),
		        COALESCE(idempotency_key,''), idempotency_record_id
		 FROM transactions WHERE ` + predicate + ` AND mode = $2`
	args := []interface{}{value, mode}
	if orgID != "" {
		query += ` AND tenant_id = $3`
		args = append(args, orgID)
	}
	err := r.readDB().QueryRow(ctx, query, args...).Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
		&tx.FromWallet, &tx.ToWallet, &tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.Mode,
		&tx.CreatedAt, &tx.RequeueCount, &tx.ReconciledAt, &batchID, &reference,
		&externalRef, &tags,
		&failureReason, &failureMessage, &idempotencyKey, &recordID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrTransactionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get transaction by idempotency reference: %w", err)
	}
	tx.Amount, _ = decimal.NewFromString(amount)
	tx.Fee, _ = decimal.NewFromString(fee)
	if feeBps != nil {
		tx.FeeBps = *feeBps
	}
	tx.TenantID = tenantID
	tx.BatchID = batchID
	tx.Reference = reference
	tx.ExternalReference = externalRef
	tx.Tags = tags
	tx.FailureReason = failureReason
	tx.FailureMessage = failureMessage
	tx.IdempotencyKey = idempotencyKey
	tx.IdempotencyRecordID = recordID
	return tx, nil
}

// ClaimForSubmission atomically transitions a transaction from pending to
// submitted.
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

// RetryFailedTransaction atomically reopens only a definitively failed
// transfer, scoped to the authenticated tenant and environment. Clearing the
// prior hash prevents a rejected attempt from being mistaken for this retry.
func (r *TransactionRepo) RetryFailedTransaction(ctx context.Context, id string) error {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return domain.ErrForbidden
	}
	mode, ok := tenant.ModeFromContext(ctx)
	if !ok {
		return domain.ErrForbidden
	}
	tx, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if tx.Status != domain.StatusFailed {
		return fmt.Errorf("retry failed transaction: %w", domain.ErrConcurrentUpdate)
	}
	tag, err := r.db.Exec(ctx,
		`UPDATE transactions
		 SET status = 'pending', tx_hash = NULL, failure_reason = '', failure_message = ''
		 WHERE id = $1 AND tenant_id = $2 AND mode = $3 AND status = 'failed'`,
		id, tenantID, mode,
	)
	if err != nil {
		return fmt.Errorf("retry failed transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("retry failed transaction: %w", domain.ErrConcurrentUpdate)
	}
	return nil
}

// ResetStuckSubmittedToPending recovers a transaction that was claimed
// (status=submitted) but never got a tx_hash recorded.
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
		 SET status = $2, tx_hash = COALESCE(NULLIF($3, ''), tx_hash)
		 WHERE id = $1 AND mode = $4
		   AND status != 'confirmed'`
	args := []interface{}{id, status, txHash, transactionMode(ctx)}
	if tID != "" {
		query += ` AND tenant_id = $5`
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
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, mode, created_at,
		        COALESCE(requeue_count, 0), reconciled_at,
		        fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
		        batch_id, COALESCE(reference,''), external_reference, COALESCE(tags, '{}'),
		        COALESCE(failure_reason,''), COALESCE(failure_message,'')
		 FROM transactions
		 WHERE (from_wallet = $1 OR to_wallet = $1) AND mode = $2`
	args := []interface{}{walletID, transactionMode(ctx)}

	if tID != "" {
		query += ` AND tenant_id = $3 ORDER BY created_at DESC LIMIT $4 OFFSET $5`
		args = append(args, tID, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $3 OFFSET $4`
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
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, mode, created_at,
		        COALESCE(requeue_count, 0), reconciled_at,
		        fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
		        batch_id, COALESCE(reference,''), external_reference, COALESCE(tags, '{}'),
		        COALESCE(failure_reason,''), COALESCE(failure_message,'')
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

func (r *TransactionRepo) ListWithFilter(ctx context.Context, filter domain.TransactionFilter) ([]*domain.Transaction, error) {
	tID := tenant.IDFromContext(ctx)
	mode := transactionMode(ctx)

	query := `SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, mode, created_at,
		        COALESCE(requeue_count, 0), reconciled_at,
		        fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount,
		        batch_id, COALESCE(reference,''), external_reference, COALESCE(tags, '{}'),
		        COALESCE(failure_reason,''), COALESCE(failure_message,'')
		 FROM transactions WHERE mode = $1`
	args := []interface{}{mode}
	idx := 2

	if tID != "" {
		query += fmt.Sprintf(" AND tenant_id = $%d", idx)
		args = append(args, tID)
		idx++
	}
	if filter.WalletID != "" {
		query += fmt.Sprintf(" AND (from_wallet = $%d OR to_wallet = $%d)", idx, idx)
		args = append(args, filter.WalletID)
		idx++
	}
	if filter.ExternalReference != "" {
		query += fmt.Sprintf(" AND external_reference = $%d", idx)
		args = append(args, filter.ExternalReference)
		idx++
	}
	if filter.Tag != "" {
		query += fmt.Sprintf(" AND $%d = ANY(tags)", idx)
		args = append(args, filter.Tag)
		idx++
	}

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", idx, idx+1)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list transactions filtered: %w", err)
	}
	defer rows.Close()

	return scanTransactions(rows)
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
		var externalRef *string
		var tags []string
		var failureReason, failureMessage string
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
			&tx.FromWallet, &tx.ToWallet,
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.Mode, &tx.CreatedAt,
			&tx.RequeueCount, &tx.ReconciledAt,
			&tx.FiatRail, &tx.FiatProviderRef, &tx.FiatStatus, &tx.LocalCurrency, &localAmt,
			&batchID, &reference, &externalRef, &tags, &failureReason, &failureMessage); err != nil {
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
		tx.ExternalReference = externalRef
		tx.Tags = tags
		tx.FailureReason = failureReason
		tx.FailureMessage = failureMessage
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
func (r *TransactionRepo) GetConfirmedTxesForReconciliation(ctx context.Context, since time.Duration, limit int) ([]*domain.Transaction, error) {
	rows, err := r.db.Query(ctx,
		`WITH claimed AS (
			SELECT id FROM transactions
			WHERE status = 'confirmed'
			  AND tx_hash IS NOT NULL
			  AND (reconciled_at IS NULL OR reconciled_at < NOW() - $1::interval)
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE transactions SET reconciled_at = NOW()
		WHERE id IN (SELECT id FROM claimed)
		RETURNING id, COALESCE(tx_hash,''), type, status,
		          COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		          asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		          COALESCE(requeue_count, 0), reconciled_at`,
		since.String(), limit,
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
// that never made it to the network.
func (r *TransactionRepo) GetStuckPendingTxes(ctx context.Context, olderThan time.Duration, limit int) ([]*domain.Transaction, error) {
	rows, err := r.db.Query(ctx,
		`WITH claimed AS (
			SELECT id FROM transactions
			WHERE (status = 'pending' OR (status = 'submitted' AND tx_hash IS NULL))
			  AND created_at < NOW() - $1::interval
			  AND (reconciled_at IS NULL OR reconciled_at < NOW() - $1::interval)
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE transactions SET reconciled_at = NOW()
		WHERE id IN (SELECT id FROM claimed)
		RETURNING id, COALESCE(tx_hash,''), type, status,
		          COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		          asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		          COALESCE(requeue_count, 0), reconciled_at, batch_id`,
		olderThan.String(), limit,
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
		var tenantID, batchID *string
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
			&tx.FromWallet, &tx.ToWallet,
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
			&tx.RequeueCount, &tx.ReconciledAt, &batchID); err != nil {
			return nil, err
		}
		tx.Amount, _ = decimal.NewFromString(amount)
		tx.Fee, _ = decimal.NewFromString(fee)
		if feeBps != nil {
			tx.FeeBps = *feeBps
		}
		tx.TenantID = tenantID
		tx.BatchID = batchID
		txs = append(txs, tx)
	}
	return txs, rows.Err()
}

// UpdateReconciliationStatus updates the status without the confirmed guard.
func (r *TransactionRepo) UpdateReconciliationStatus(ctx context.Context, id string, status domain.TransactionStatus) error {
	tID := tenant.IDFromContext(ctx)
	query := `UPDATE transactions SET status = $2 WHERE id = $1 AND status != $2`
	args := []interface{}{id, status}

	if tID != "" {
		query += ` AND tenant_id = $3`
		args = append(args, tID)
	}

	tag, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update reconciliation status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("update reconciliation status: %w", domain.ErrConcurrentUpdate)
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
func (r *TransactionRepo) WriteAuditLog(ctx context.Context, entry *domain.AuditLogEntry) error {
	err := RunInTx(ctx, r.db, func(txCtx context.Context) error {
		db := TxFromContext(txCtx, r.db)
		_, err := db.Exec(txCtx,
			`INSERT INTO ledger_audit_log (id, tx_id, stellar_hash, checked_at, horizon_status, amount_verified, asset_verified, fee_verified, outcome, details)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			entry.ID, entry.TxID, entry.StellarHash, entry.CheckedAt,
			entry.HorizonStatus, entry.AmountVerified, entry.AssetVerified, entry.FeeVerified,
			entry.Outcome, entry.Details,
		)
		if err != nil {
			return fmt.Errorf("write audit log: %w", err)
		}
		if entry.Outcome == domain.AuditOK {
			return nil
		}
		category := domain.ReconciliationDiscrepancyCategory(entry)
		_, err = db.Exec(txCtx,
			`INSERT INTO reconciliation_discrepancies
			 (tenant_id, transaction_id, category, last_audit_log_id)
			 SELECT tenant_id, id, $2, $3 FROM transactions WHERE id = $1 AND tenant_id IS NOT NULL
			 ON CONFLICT (transaction_id, category) DO UPDATE
			 SET last_audit_log_id = EXCLUDED.last_audit_log_id,
			     status = CASE WHEN reconciliation_discrepancies.status = 'resolved' THEN 'open' ELSE reconciliation_discrepancies.status END,
			     resolved_at = CASE WHEN reconciliation_discrepancies.status = 'resolved' THEN NULL ELSE reconciliation_discrepancies.resolved_at END,
			     updated_at = NOW()`,
			entry.TxID, category, entry.ID,
		)
		if err != nil {
			return fmt.Errorf("upsert reconciliation discrepancy: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("write reconciliation audit result: %w", err)
	}
	return nil
}

// GetDailyReconciliationSummary returns counts grouped by day for the last 7 days.
func (r *TransactionRepo) GetDailyReconciliationSummary(ctx context.Context, days int) ([]domain.DailySummaryRow, error) {
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

	var summary []domain.DailySummaryRow
	for rows.Next() {
		var row domain.DailySummaryRow
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
func (r *TransactionRepo) UpsertByTxHash(ctx context.Context, tx *domain.Transaction) error {
	if tx.Mode == "" {
		tx.Mode = transactionMode(ctx)
	}
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		tx.TenantID = &tID
	}
	if tx.Tags == nil {
		tx.Tags = []string{}
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, mode, created_at, requeue_count, reconciled_at, fiat_rail, fiat_provider_ref, fiat_status, local_currency, local_amount, batch_id, reference, external_reference, tags, idempotency_key, idempotency_record_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26)
		 ON CONFLICT (mode, tx_hash) WHERE tx_hash IS NOT NULL DO NOTHING`,
		tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
		nullableString(tx.FromWallet), nullableString(tx.ToWallet),
		tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
		nullableUUID(tx.TenantID), tx.Mode, tx.CreatedAt,
		tx.RequeueCount, nullableTime(tx.ReconciledAt),
		nullableStringPtr(tx.FiatRail), nullableStringPtr(tx.FiatProviderRef),
		nullableStringPtr(tx.FiatStatus), nullableStringPtr(tx.LocalCurrency),
		nullableDecimalPtr(tx.LocalAmount),
		nullableUUID(tx.BatchID), nullableString(tx.Reference),
		nullableStringPtr(tx.ExternalReference), tx.Tags,
		nullableString(tx.IdempotencyKey), nullableUUID(tx.IdempotencyRecordID),
	)
	if err != nil {
		return fmt.Errorf("upsert transaction by tx_hash: %w", err)
	}
	return nil
}

// GetPendingTxesForReconciliation returns pending or submitted transactions
// that have a Stellar tx_hash stored and are older than olderThan.
func (r *TransactionRepo) GetPendingTxesForReconciliation(ctx context.Context, olderThan time.Duration, limit int) ([]*domain.Transaction, error) {
	rows, err := r.db.Query(ctx,
		`WITH claimed AS (
			SELECT id FROM transactions
			WHERE status IN ('pending', 'submitted')
			  AND tx_hash IS NOT NULL
			  AND ((reconciled_at IS NULL AND created_at < NOW() - $1::interval) OR
			       (reconciled_at IS NOT NULL AND reconciled_at < NOW() - $1::interval))
			ORDER BY created_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE transactions SET reconciled_at = NOW()
		WHERE id IN (SELECT id FROM claimed)
		RETURNING id, COALESCE(tx_hash,''), type, status,
		          COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		          asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		          COALESCE(requeue_count, 0), reconciled_at, batch_id`,
		olderThan.String(), limit,
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
		var batchID *string
		if err := rows.Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
			&tx.FromWallet, &tx.ToWallet,
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
			&tx.RequeueCount, &tx.ReconciledAt, &batchID); err != nil {
			return nil, err
		}
		tx.Amount, _ = decimal.NewFromString(amount)
		tx.Fee, _ = decimal.NewFromString(fee)
		if feeBps != nil {
			tx.FeeBps = *feeBps
		}
		tx.TenantID = tenantID
		tx.BatchID = batchID
		txs = append(txs, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return txs, nil
}

// UpdateTxConfirmed transitions a pending or submitted transaction to confirmed.
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
func (r *TransactionRepo) WriteReconciliationRun(ctx context.Context, run *domain.ReconciliationRun) error {
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
		`SELECT COUNT(*) FROM transactions WHERE tenant_id = $1 AND mode = $2 AND created_at >= $3 AND created_at < $4`,
		tenantID, transactionMode(ctx), startDate, endDate,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count monthly transfers: %w", err)
	}
	return count, nil
}

func (r *TransactionRepo) CountDailyTransfersByTenant(ctx context.Context, tenantID string, date time.Time) (int, error) {
	startDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 0, 1)

	var count int
	err := r.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM transactions WHERE tenant_id = $1 AND mode = $2 AND created_at >= $3 AND created_at < $4`,
		tenantID, transactionMode(ctx), startDate, endDate,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count daily transfers: %w", err)
	}
	return count, nil
}

// CreateWithMonthlyLimit atomically checks the tenant's monthly transfer count
// and inserts the transaction in a single database transaction. When the
// caller already runs inside RunInTx — a batch persisting all of its items at
// once — the check and insert join that ambient transaction instead of
// opening a second one: the batch's earlier rows are visible to the count and
// the whole batch commits or rolls back as a single unit.
func (r *TransactionRepo) CreateWithMonthlyLimit(ctx context.Context, tx *domain.Transaction, tenantID string, year int, month time.Month, limit int) error {
	startDate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	insert := func(txCtx context.Context, db DB) error {
		var locked int
		err := db.QueryRow(txCtx, `SELECT 1 FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&locked)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock tenant for limit check: %w", err)
		}

		var count int
		err = db.QueryRow(txCtx,
			`SELECT COUNT(*) FROM transactions WHERE tenant_id = $1 AND mode = $2 AND created_at >= $3 AND created_at < $4`,
			tenantID, transactionMode(txCtx), startDate, endDate,
		).Scan(&count)
		if err != nil {
			return fmt.Errorf("count monthly transfers: %w", err)
		}

		if count >= limit {
			return domain.ErrTransferLimitReached
		}

		if tx.Tags == nil {
			tx.Tags = []string{}
		}
		_, err = db.Exec(txCtx,
			`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, mode, created_at, requeue_count, reconciled_at, batch_id, reference, external_reference, tags, idempotency_key, idempotency_record_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
			tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
			nullableString(tx.FromWallet), nullableString(tx.ToWallet),
			tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
			nullableUUID(tx.TenantID), transactionMode(txCtx), tx.CreatedAt,
			tx.RequeueCount, nullableTime(tx.ReconciledAt),
			nullableUUID(tx.BatchID), nullableString(tx.Reference),
			nullableStringPtr(tx.ExternalReference), tx.Tags,
			nullableString(tx.IdempotencyKey), nullableUUID(tx.IdempotencyRecordID),
		)
		if err != nil {
			return fmt.Errorf("insert transaction: %w", mapTransactionInsertError(err))
		}
		return nil
	}

	if amb, ok := AmbientTx(ctx); ok {
		return insert(ctx, amb)
	}
	dbTx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin limit-check tx: %w", err)
	}
	defer func() { _ = dbTx.Rollback(ctx) }()

	if err := insert(ctx, dbTx); err != nil {
		return err
	}
	if err := dbTx.Commit(ctx); err != nil {
		return fmt.Errorf("commit limit-check tx: %w", err)
	}
	return nil
}

// CreateWithDailyLimit is the daily-window sibling of CreateWithMonthlyLimit
// and joins an ambient caller transaction the same way.
func (r *TransactionRepo) CreateWithDailyLimit(ctx context.Context, tx *domain.Transaction, tenantID string, date time.Time, limit int) error {
	startDate := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 0, 1)

	insert := func(txCtx context.Context, db DB) error {
		var locked int
		err := db.QueryRow(txCtx, `SELECT 1 FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&locked)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("lock tenant for daily limit check: %w", err)
		}

		var count int
		err = db.QueryRow(txCtx,
			`SELECT COUNT(*) FROM transactions WHERE tenant_id = $1 AND mode = $2 AND created_at >= $3 AND created_at < $4`,
			tenantID, transactionMode(txCtx), startDate, endDate,
		).Scan(&count)
		if err != nil {
			return fmt.Errorf("count daily transfers: %w", err)
		}

		if count >= limit {
			return domain.ErrDailyTransferLimitReached
		}

		if tx.Tags == nil {
			tx.Tags = []string{}
		}
		_, err = db.Exec(txCtx,
			`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, mode, created_at, requeue_count, reconciled_at, batch_id, reference, external_reference, tags, idempotency_key, idempotency_record_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`,
			tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
			nullableString(tx.FromWallet), nullableString(tx.ToWallet),
			tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
			nullableUUID(tx.TenantID), transactionMode(txCtx), tx.CreatedAt,
			tx.RequeueCount, nullableTime(tx.ReconciledAt),
			nullableUUID(tx.BatchID), nullableString(tx.Reference),
			nullableStringPtr(tx.ExternalReference), tx.Tags,
			nullableString(tx.IdempotencyKey), nullableUUID(tx.IdempotencyRecordID),
		)
		if err != nil {
			return fmt.Errorf("insert transaction: %w", mapTransactionInsertError(err))
		}
		return nil
	}

	if amb, ok := AmbientTx(ctx); ok {
		return insert(ctx, amb)
	}
	dbTx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin daily limit-check tx: %w", err)
	}
	defer func() { _ = dbTx.Rollback(ctx) }()

	if err := insert(ctx, dbTx); err != nil {
		return err
	}
	if err := dbTx.Commit(ctx); err != nil {
		return fmt.Errorf("commit daily limit-check tx: %w", err)
	}
	return nil
}
