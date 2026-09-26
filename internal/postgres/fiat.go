package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
)

type FiatRepo struct {
	db DB
}

func NewFiatRepo(db DB) *FiatRepo {
	return &FiatRepo{db: db}
}

func (r *FiatRepo) CreateDeposit(ctx context.Context, d *domain.FiatDeposit) error {
	query := `
		INSERT INTO fiat_deposits (id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, status, instructions, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	instructionsJSON, _ := json.Marshal(d.Instructions)
	_, err := r.db.Exec(ctx, query,
		d.ID, d.WalletID, d.Provider, d.ProviderReference,
		d.FiatAmount, d.FiatCurrency, d.USDCAmount, d.Status, instructionsJSON, d.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert fiat deposit: %w", err)
	}
	return nil
}

func (r *FiatRepo) UpdateDepositStatus(ctx context.Context, id, status string) error {
	// Allows the pending->processing->{completed,failed} lifecycle: a
	// deposit may be moved as long as it hasn't already reached a terminal
	// state. Terminal deposits are left untouched.
	query := `UPDATE fiat_deposits SET status = $1 WHERE id = $2 AND status NOT IN ('completed', 'failed')`
	tag, err := r.db.Exec(ctx, query, status, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deposit %s already processed or terminal", id)
	}
	return nil
}

// ClaimDepositForProcessing atomically transitions a deposit from pending to
// processing. The WHERE clause makes this a single conditional UPDATE: under
// concurrent/duplicate webhook deliveries for the same deposit, the database
// serializes the two UPDATEs against the same row and only one can affect
// it — the loser gets RowsAffected() == 0 and must not move any funds.
func (r *FiatRepo) ClaimDepositForProcessing(ctx context.Context, id string) error {
	query := `UPDATE fiat_deposits SET status = $1 WHERE id = $2 AND status = $3`
	tag, err := r.db.Exec(ctx, query, domain.FiatStatusProcessing, id, domain.FiatStatusPending)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("deposit %s already claimed or not pending", id)
	}
	return nil
}

func (r *FiatRepo) UpdateDepositInstructions(ctx context.Context, id string, instructions map[string]string) error {
	instructionsJSON, _ := json.Marshal(instructions)
	query := `UPDATE fiat_deposits SET instructions = $1 WHERE id = $2`
	_, err := r.db.Exec(ctx, query, instructionsJSON, id)
	return err
}

func (r *FiatRepo) GetDepositByReference(ctx context.Context, ref string) (*domain.FiatDeposit, error) {
	query := `
		SELECT id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, COALESCE(instructions, '{}'), status, created_at
		FROM fiat_deposits WHERE provider_reference = $1
	`
	var d domain.FiatDeposit
	var instructionsJSON []byte
	err := r.db.QueryRow(ctx, query, ref).Scan(
		&d.ID, &d.WalletID, &d.Provider, &d.ProviderReference,
		&d.FiatAmount, &d.FiatCurrency, &d.USDCAmount, &instructionsJSON, &d.Status, &d.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("deposit not found")
		}
		return nil, err
	}
	json.Unmarshal(instructionsJSON, &d.Instructions)
	return &d, nil
}

func (r *FiatRepo) GetDepositByID(ctx context.Context, id string) (*domain.FiatDeposit, error) {
	query := `
		SELECT id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, COALESCE(instructions, '{}'), status, created_at
		FROM fiat_deposits WHERE id = $1
	`
	var d domain.FiatDeposit
	var instructionsJSON []byte
	err := r.db.QueryRow(ctx, query, id).Scan(
		&d.ID, &d.WalletID, &d.Provider, &d.ProviderReference,
		&d.FiatAmount, &d.FiatCurrency, &d.USDCAmount, &instructionsJSON, &d.Status, &d.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("deposit not found")
		}
		return nil, err
	}
	json.Unmarshal(instructionsJSON, &d.Instructions)
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
	query := `UPDATE fiat_withdrawals SET status = $1 WHERE id = $2 AND status = 'pending'`
	tag, err := r.db.Exec(ctx, query, status, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("withdrawal %s already processed or not pending", id)
	}
	return nil
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

func (r *FiatRepo) GetWithdrawalByID(ctx context.Context, id string) (*domain.FiatWithdrawal, error) {
	query := `
		SELECT id, wallet_id, provider, provider_reference, fiat_amount, fiat_currency, usdc_amount, status, created_at
		FROM fiat_withdrawals WHERE id = $1
	`
	var w domain.FiatWithdrawal
	err := r.db.QueryRow(ctx, query, id).Scan(
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
