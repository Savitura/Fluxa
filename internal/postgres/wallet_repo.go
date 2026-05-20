package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WalletRepo struct {
	db *pgxpool.Pool
}

func NewWalletRepo(db *pgxpool.Pool) *WalletRepo {
	return &WalletRepo{db: db}
}

func (r *WalletRepo) Create(ctx context.Context, w *domain.Wallet) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO wallets (id, public_key, encrypted_secret, created_at)
		 VALUES ($1, $2, $3, $4)`,
		w.ID, w.PublicKey, w.EncryptedSecret, w.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert wallet: %w", err)
	}
	return nil
}

func (r *WalletRepo) GetByID(ctx context.Context, id string) (*domain.Wallet, error) {
	w := &domain.Wallet{}
	err := r.db.QueryRow(ctx,
		`SELECT id, public_key, encrypted_secret, created_at FROM wallets WHERE id = $1`,
		id,
	).Scan(&w.ID, &w.PublicKey, &w.EncryptedSecret, &w.CreatedAt)
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
	err := r.db.QueryRow(ctx,
		`SELECT id, public_key, encrypted_secret, created_at FROM wallets WHERE public_key = $1`,
		pubKey,
	).Scan(&w.ID, &w.PublicKey, &w.EncryptedSecret, &w.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWalletNotFound
		}
		return nil, fmt.Errorf("get wallet by public key: %w", err)
	}
	return w, nil
}

func (r *WalletRepo) List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, public_key, encrypted_secret, created_at FROM wallets
		 ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	defer rows.Close()

	var wallets []*domain.Wallet
	for rows.Next() {
		w := &domain.Wallet{}
		if err := rows.Scan(&w.ID, &w.PublicKey, &w.EncryptedSecret, &w.CreatedAt); err != nil {
			return nil, err
		}
		wallets = append(wallets, w)
	}
	return wallets, rows.Err()
}
