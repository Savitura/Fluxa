package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
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
		`INSERT INTO transactions (id, tx_hash, type, status, from_wallet, to_wallet, asset, amount, fee, fee_bps, tenant_id, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		tx.ID, nullableString(tx.TxHash), tx.Type, tx.Status,
		nullableString(tx.FromWallet), nullableString(tx.ToWallet),
		tx.Asset, tx.Amount.String(), tx.Fee.String(), nullableFeeBps(tx.FeeBps),
		nullableUUID(tx.TenantID), tx.CreatedAt,
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
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at
		 FROM transactions WHERE id = $1`,
		id,
	).Scan(&tx.ID, &tx.TxHash, &tx.Type, &tx.Status,
		&tx.FromWallet, &tx.ToWallet,
		&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt)
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
		        asset, amount, COALESCE(fee,'0'), fee_bps, tenant_id, created_at
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
			&tx.Asset, &amount, &fee, &feeBps, &tenantID, &tx.CreatedAt); err != nil {
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
