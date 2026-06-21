package postgres

import (
	"context"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FiatRepo struct {
	db *pgxpool.Pool
}

func NewFiatRepo(db *pgxpool.Pool) *FiatRepo {
	return &FiatRepo{db: db}
}

func (r *FiatRepo) CreateDeposit(ctx context.Context, d *domain.FiatDeposit) error {
	query := `
		INSERT INTO fiat_deposits (id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := r.db.Exec(ctx, query,
		d.ID, d.WalletID, d.Provider, d.ProviderReference,
		d.FiatAmount, d.FiatCurrency, d.USDCAmount, d.Status, d.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert fiat deposit: %w", err)
	}
	return nil
}

func (r *FiatRepo) UpdateDepositStatus(ctx context.Context, id, status string) error {
	query := `UPDATE fiat_deposits SET status = $1 WHERE id = $2`
	_, err := r.db.Exec(ctx, query, status, id)
	return err
}

func (r *FiatRepo) GetDepositByReference(ctx context.Context, ref string) (*domain.FiatDeposit, error) {
	query := `
		SELECT id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, status, created_at
		FROM fiat_deposits WHERE provider_reference = $1
	`
	var d domain.FiatDeposit
	err := r.db.QueryRow(ctx, query, ref).Scan(
		&d.ID, &d.WalletID, &d.Provider, &d.ProviderReference,
		&d.FiatAmount, &d.FiatCurrency, &d.USDCAmount, &d.Status, &d.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("deposit not found")
		}
		return nil, err
	}
	return &d, nil
}

func (r *FiatRepo) CreateWithdrawal(ctx context.Context, w *domain.FiatWithdrawal) error {
	query := `
		INSERT INTO fiat_withdrawals (id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := r.db.Exec(ctx, query,
		w.ID, w.WalletID, w.Provider, w.ProviderReference,
		w.FiatAmount, w.FiatCurrency, w.USDCAmount, w.Status, w.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert fiat withdrawal: %w", err)
	}
	return nil
}

func (r *FiatRepo) UpdateWithdrawalStatus(ctx context.Context, id, status string) error {
	query := `UPDATE fiat_withdrawals SET status = $1 WHERE id = $2`
	_, err := r.db.Exec(ctx, query, status, id)
	return err
}

func (r *FiatRepo) GetWithdrawalByReference(ctx context.Context, ref string) (*domain.FiatWithdrawal, error) {
	query := `
		SELECT id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, status, created_at
		FROM fiat_withdrawals WHERE provider_reference = $1
	`
	var w domain.FiatWithdrawal
	err := r.db.QueryRow(ctx, query, ref).Scan(
		&w.ID, &w.WalletID, &w.Provider, &w.ProviderReference,
		&w.FiatAmount, &w.FiatCurrency, &w.USDCAmount, &w.Status, &w.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("withdrawal not found")
		}
		return nil, err
	}
	return &w, nil
}
