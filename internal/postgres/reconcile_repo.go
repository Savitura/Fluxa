package postgres

import (
	"context"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/shopspring/decimal"
)

// ReconcileRepo implements reconcile.WalletRepository, covering wallet listing,
// DB balance reads, and balance discrepancy persistence.
type ReconcileRepo struct {
	db DB
}

func NewReconcileRepo(db DB) *ReconcileRepo {
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

// GetDBBalances returns all balances for a wallet keyed by canonical asset
// identity: "XLM" for native assets, "CODE:ISSUER" for credit assets. This
// ensures two issuers sharing the same asset code are compared independently.
func (r *ReconcileRepo) GetDBBalances(ctx context.Context, walletID string) (map[string]decimal.Decimal, error) {
	rows, err := r.db.Query(ctx,
		`SELECT asset_code, issuer, balance FROM balances WHERE wallet_id = $1`,
		walletID,
	)
	if err != nil {
		return nil, fmt.Errorf("get DB balances for wallet %s: %w", walletID, err)
	}
	defer rows.Close()

	balances := make(map[string]decimal.Decimal)
	for rows.Next() {
		var assetCode, issuer, balance string
		if err := rows.Scan(&assetCode, &issuer, &balance); err != nil {
			return nil, err
		}
		amt, _ := decimal.NewFromString(balance)
		key := assetCode
		if issuer != "" {
			key = assetCode + ":" + issuer
		}
		balances[key] = amt
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

func (r *ReconcileRepo) WriteDriftSnapshot(ctx context.Context, snapshot *reconcile.DriftSnapshot) error {
	var tenantID *string
	if snapshot.TenantID != "" {
		tenantID = &snapshot.TenantID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO reconciliation_drift_snapshots
		 (id, tenant_id, wallet_id, wallet_address, asset, expected_balance, actual_balance, drift_amount, threshold, detected_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		snapshot.ID, tenantID, snapshot.WalletID, snapshot.WalletAddress, snapshot.Asset,
		snapshot.ExpectedBalance.String(), snapshot.ActualBalance.String(), snapshot.DriftAmount.String(),
		snapshot.Threshold.String(), snapshot.DetectedAt,
	)
	if err != nil {
		return fmt.Errorf("write reconciliation drift snapshot: %w", err)
	}
	return nil
}

func (r *ReconcileRepo) ListCurrentDrift(ctx context.Context) ([]*reconcile.DriftSnapshot, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tenant_id, wallet_id, wallet_address, asset, expected_balance, actual_balance, drift_amount, threshold, detected_at
		 FROM (
		 SELECT id, tenant_id, wallet_id, wallet_address, asset, expected_balance, actual_balance, drift_amount, threshold, detected_at,
		        ROW_NUMBER() OVER (PARTITION BY wallet_id, asset ORDER BY detected_at DESC, id DESC) AS row_number
		 FROM reconciliation_drift_snapshots
		 ) latest
		 WHERE row_number = 1
		 ORDER BY detected_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list current reconciliation drift: %w", err)
	}
	defer rows.Close()

	var snapshots []*reconcile.DriftSnapshot
	for rows.Next() {
		snapshot := &reconcile.DriftSnapshot{}
		var tenantID *string
		var expected, actual, drift, threshold string
		if err := rows.Scan(
			&snapshot.ID, &tenantID, &snapshot.WalletID, &snapshot.WalletAddress, &snapshot.Asset,
			&expected, &actual, &drift, &threshold, &snapshot.DetectedAt,
		); err != nil {
			return nil, err
		}
		if tenantID != nil {
			snapshot.TenantID = *tenantID
		}
		snapshot.ExpectedBalance, err = decimal.NewFromString(expected)
		if err != nil {
			return nil, err
		}
		snapshot.ActualBalance, err = decimal.NewFromString(actual)
		if err != nil {
			return nil, err
		}
		snapshot.DriftAmount, err = decimal.NewFromString(drift)
		if err != nil {
			return nil, err
		}
		snapshot.Threshold, err = decimal.NewFromString(threshold)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

var _ reconcile.DriftRepository = (*ReconcileRepo)(nil)
