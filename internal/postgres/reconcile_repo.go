package postgres

import (
	"context"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// ReconcileRepo implements reconcile.WalletRepository, covering wallet listing,
// DB balance reads, and balance discrepancy persistence.
type ReconcileRepo struct {
	db *pgxpool.Pool
}

func NewReconcileRepo(db *pgxpool.Pool) *ReconcileRepo {
	return &ReconcileRepo{db: db}
}

// ListAllWallets returns every wallet without pagination. Used by the daily
// balance reconciliation job which must inspect the full wallet set.
func (r *ReconcileRepo) ListAllWallets(ctx context.Context) ([]*domain.Wallet, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, public_key, encrypted_secret, tenant_id, created_at FROM wallets ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list all wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*domain.Wallet
	for rows.Next() {
		w := &domain.Wallet{}
		if err := rows.Scan(&w.ID, &w.PublicKey, &w.EncryptedSecret, &w.TenantID, &w.CreatedAt); err != nil {
			return nil, err
		}
		wallets = append(wallets, w)
	}
	return wallets, rows.Err()
}

// GetDBBalances returns all balances for a wallet keyed by asset code (e.g. "XLM",
// "USDC"). An asset absent from the table is represented as zero.
func (r *ReconcileRepo) GetDBBalances(ctx context.Context, walletID string) (map[string]decimal.Decimal, error) {
	rows, err := r.db.Query(ctx,
		`SELECT asset_code, balance FROM balances WHERE wallet_id = $1`,
		walletID,
	)
	if err != nil {
		return nil, fmt.Errorf("get DB balances for wallet %s: %w", walletID, err)
	}
	defer rows.Close()

	balances := make(map[string]decimal.Decimal)
	for rows.Next() {
		var assetCode, balance string
		if err := rows.Scan(&assetCode, &balance); err != nil {
			return nil, err
		}
		amt, _ := decimal.NewFromString(balance)
		balances[assetCode] = amt
	}
	return balances, rows.Err()
}

// WriteBalanceDiscrepancy inserts a detected balance discrepancy for manual review.
// Auto-correction is intentionally not performed here.
func (r *ReconcileRepo) WriteBalanceDiscrepancy(ctx context.Context, d *reconcile.BalanceDiscrepancy) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO balance_discrepancies (id, wallet_id, db_balance, horizon_balance, asset, detected_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		d.ID, d.WalletID, d.DBBalance.String(), d.HorizonBalance.String(), d.Asset, d.DetectedAt,
	)
	if err != nil {
		return fmt.Errorf("write balance discrepancy: %w", err)
	}
	return nil
}
