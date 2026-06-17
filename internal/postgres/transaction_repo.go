package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type TransactionRepo struct {
	db *pgxpool.Pool
}

func NewTransactionRepo(db *pgxpool.Pool) *TransactionRepo {
	return &TransactionRepo{db: db}
}

func (r *TransactionRepo) Create(ctx context.Context, tx *domain.Transaction) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, created_at, requeue_count, reconciled_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
		nullableString(tx.FromWallet), nullableString(tx.ToWallet),
		tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
		nullableUUID(tx.TenantID), tx.CreatedAt,
		tx.RequeueCount, nullableTime(tx.ReconciledAt),
	)
	if err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}
	return nil
}

func (r *TransactionRepo) GetByID(ctx context.Context, id string) (*domain.Transaction, error) {
	tx := &domain.Transaction{}
	var amount, fee string
	var feeBps *int
	var tenantID *string
	err := r.db.QueryRow(ctx,
		`SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at
		 FROM transactions WHERE id = $1`,
		id,
	).Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
		&tx.FromWallet, &tx.ToWallet,
		&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt,
		&tx.RequeueCount, &tx.ReconciledAt)
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
	return tx, nil
}

func (r *TransactionRepo) UpdateStatus(ctx context.Context, id string, status domain.TransactionStatus, txHash string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE transactions
		 SET status = $2, tx_hash = NULLIF($3, '')
		 WHERE id = $1
		   AND status != 'confirmed'`,
		id, status, txHash,
	)
	if err != nil {
		return fmt.Errorf("update transaction status: %w", err)
	}
	return nil
}

func (r *TransactionRepo) ListByWallet(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, COALESCE(tx_hash,''), type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at
		 FROM transactions
		 WHERE from_wallet = $1 OR to_wallet = $1
		 ORDER BY created_at DESC
		 LIMIT $2 OFFSET $3`,
		walletID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list transactions: %w", err)
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

// GetStuckPendingTxes returns pending transactions older than the specified duration.
func (r *TransactionRepo) GetStuckPendingTxes(ctx context.Context, olderThan time.Duration) ([]*domain.Transaction, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tx_hash, type, status,
		        COALESCE(from_wallet::text,''), COALESCE(to_wallet::text,''),
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at,
		        COALESCE(requeue_count, 0), reconciled_at
		 FROM transactions
		 WHERE status = 'pending'
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
		`INSERT INTO ledger_audit_log (id, tx_id, stellar_hash, checked_at, horizon_status, amount_verified, asset_verified, outcome, details)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		entry.ID, entry.TxID, entry.StellarHash, entry.CheckedAt,
		entry.HorizonStatus, entry.AmountVerified, entry.AssetVerified,
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
