package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type WalletRepo struct {
	db *pgxpool.Pool
}

func NewWalletRepo(db *pgxpool.Pool) *WalletRepo {
	return &WalletRepo{db: db}
}

func (r *WalletRepo) Create(ctx context.Context, w *domain.Wallet) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		w.TenantID = &tID
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO wallets (id, public_key, encrypted_secret, tenant_id, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		w.ID, w.PublicKey, w.EncryptedSecret, nullableUUID(w.TenantID), w.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert wallet: %w", err)
	}
	return nil
}

func (r *WalletRepo) GetByID(ctx context.Context, id string) (*domain.Wallet, error) {
	w := &domain.Wallet{}
	tID := tenant.IDFromContext(ctx)

	query := `SELECT id, public_key, encrypted_secret, tenant_id, created_at, sync_cursor FROM wallets WHERE id = $1`
	args := []interface{}{id}
	if tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}

	err := r.db.QueryRow(ctx, query, args...).Scan(&w.ID, &w.PublicKey, &w.EncryptedSecret, &w.TenantID, &w.CreatedAt, &w.SyncCursor)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWalletNotFound
		}
		return nil, fmt.Errorf("get wallet by id: %w", err)
	}
	return w, nil
}

func (r *WalletRepo) GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error) {
	w := &domain.Wallet{}
	tID := tenant.IDFromContext(ctx)

	query := `SELECT id, public_key, encrypted_secret, tenant_id, created_at, sync_cursor FROM wallets WHERE public_key = $1`
	args := []interface{}{pubKey}
	if tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}

	err := r.db.QueryRow(ctx, query, args...).Scan(&w.ID, &w.PublicKey, &w.EncryptedSecret, &w.TenantID, &w.CreatedAt, &w.SyncCursor)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWalletNotFound
		}
		return nil, fmt.Errorf("get wallet by public key: %w", err)
	}
	return w, nil
}

func (r *WalletRepo) List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error) {
	tID := tenant.IDFromContext(ctx)

	query := `SELECT id, public_key, encrypted_secret, tenant_id, created_at, sync_cursor FROM wallets`
	args := []interface{}{}
	if tID != "" {
		query += ` WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = []interface{}{tID, limit, offset}
	} else {
		query += ` ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		args = []interface{}{limit, offset}
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*domain.Wallet
	for rows.Next() {
		w := &domain.Wallet{}
		if err := rows.Scan(&w.ID, &w.PublicKey, &w.EncryptedSecret, &w.TenantID, &w.CreatedAt, &w.SyncCursor); err != nil {
			return nil, err
		}
		wallets = append(wallets, w)
	}
	return wallets, rows.Err()
}

// UpsertBalance persists the current on-chain balance for a wallet/asset pair,
// overwriting any previously stored value.
func (r *WalletRepo) UpsertBalance(ctx context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO balances (wallet_id, asset_code, issuer, balance, updated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (wallet_id, asset_code, issuer)
		 DO UPDATE SET balance = EXCLUDED.balance, updated_at = NOW()`,
		walletID, assetCode, issuer, balance.String(),
	)
	if err != nil {
		return fmt.Errorf("upsert balance: %w", err)
	}
	return nil
}

// UpdateSyncCursor advances the Horizon paging token used to resume incremental sync.
func (r *WalletRepo) UpdateSyncCursor(ctx context.Context, walletID, cursor string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE wallets SET sync_cursor = $2 WHERE id = $1`,
		walletID, cursor,
	)
	if err != nil {
		return fmt.Errorf("update sync cursor: %w", err)
	}
	return nil
}
