package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type BatchRepo struct {
	db DB
}

func NewBatchRepo(db DB) *BatchRepo {
	return &BatchRepo{db: db}
}

const batchColumns = `id, tenant_id, status, total_count, succeeded_count, failed_count, settled_at, created_at, updated_at`

func scanBatch(row pgx.Row) (*domain.BatchTransfer, error) {
	b := &domain.BatchTransfer{}
	err := row.Scan(&b.ID, &b.TenantID, &b.Status, &b.TotalCount, &b.SucceededCount,
		&b.FailedCount, &b.SettledAt, &b.CreatedAt, &b.UpdatedAt)
	return b, err
}

func (r *BatchRepo) Create(ctx context.Context, b *domain.BatchTransfer) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		b.TenantID = &tID
	}
	_, err := TxFromContext(ctx, r.db).Exec(ctx,
		`INSERT INTO batch_transfers (`+batchColumns+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		b.ID, nullableUUID(b.TenantID), b.Status, b.TotalCount, b.SucceededCount,
		b.FailedCount, nullableTime(b.SettledAt), b.CreatedAt, b.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert batch: %w", err)
	}
	return nil
}

func (r *BatchRepo) GetByID(ctx context.Context, id string) (*domain.BatchTransfer, error) {
	query := `SELECT ` + batchColumns + ` FROM batch_transfers WHERE id = $1`
	args := []interface{}{id}
	if tID := tenant.IDFromContext(ctx); tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}

	b, err := scanBatch(r.db.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrBatchNotFound
		}
		return nil, fmt.Errorf("get batch by id: %w", err)
	}
	return b, nil
}

// SyncAggregates recomputes a batch's aggregate counts and status from its
// linked transactions and persists them under a row lock. The lock
// serialises concurrent settlement workers settling sibling transfers, so
// their count updates cannot overwrite each other and the move into a
// terminal status is observed by exactly one caller — which is what makes
// the batch.completed / batch.partially_failed webhook fire exactly once.
// The second result reports whether this call transitioned the batch from a
// non-terminal to a terminal status; it is false on every subsequent sync of
// an already-terminal batch.
func (r *BatchRepo) SyncAggregates(ctx context.Context, id string) (*domain.BatchTransfer, bool, error) {
	var out *domain.BatchTransfer
	transitioned := false

	err := RunInTx(ctx, r.db, func(txCtx context.Context) error {
		db := TxFromContext(txCtx, r.db)

		query := `SELECT ` + batchColumns + ` FROM batch_transfers WHERE id = $1`
		args := []interface{}{id}
		if tID := tenant.IDFromContext(txCtx); tID != "" {
			query += ` AND tenant_id = $2`
			args = append(args, tID)
		}
		query += ` FOR UPDATE`

		b, err := scanBatch(db.QueryRow(txCtx, query, args...))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrBatchNotFound
			}
			return fmt.Errorf("lock batch for sync: %w", err)
		}

		// The batch row we hold proves ownership, so the children are read
		// without a second tenant predicate.
		rows, err := db.Query(txCtx, `SELECT status FROM transactions WHERE batch_id = $1`, id)
		if err != nil {
			return fmt.Errorf("load batch transaction statuses: %w", err)
		}
		var txs []*domain.Transaction
		for rows.Next() {
			var status domain.TransactionStatus
			if err := rows.Scan(&status); err != nil {
				rows.Close()
				return fmt.Errorf("scan batch transaction status: %w", err)
			}
			txs = append(txs, &domain.Transaction{Status: status})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate batch transaction statuses: %w", err)
		}

		status, succeeded, failed, _ := domain.SummarizeBatch(txs)
		settledAt := b.SettledAt
		if domain.IsTerminalBatchStatus(status) && settledAt == nil {
			now := time.Now().UTC()
			settledAt = &now
		}

		if status != b.Status || succeeded != b.SucceededCount || failed != b.FailedCount || settledAt != b.SettledAt {
			updatedAt := time.Now().UTC()
			if _, err := db.Exec(txCtx,
				`UPDATE batch_transfers
				 SET status = $2, succeeded_count = $3, failed_count = $4, settled_at = $5, updated_at = $6
				 WHERE id = $1`,
				id, status, succeeded, failed, settledAt, updatedAt,
			); err != nil {
				return fmt.Errorf("update batch aggregates: %w", err)
			}
			if !domain.IsTerminalBatchStatus(b.Status) && domain.IsTerminalBatchStatus(status) {
				transitioned = true
			}
			b.Status = status
			b.SucceededCount = succeeded
			b.FailedCount = failed
			b.SettledAt = settledAt
			b.UpdatedAt = updatedAt
		}
		out = b
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, transitioned, nil
}
