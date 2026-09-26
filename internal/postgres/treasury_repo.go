package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type TreasuryRepo struct {
	db DB
}

func NewTreasuryRepo(db DB) *TreasuryRepo {
	return &TreasuryRepo{db: db}
}

func (r *TreasuryRepo) GetConfig(ctx context.Context, asset string) (*treasury.Config, error) {
	cfg := &treasury.Config{}
	var sweepThreshold, minBuffer string

	err := r.db.QueryRow(ctx,
		`SELECT asset, sweep_threshold, min_operating_buffer, cold_storage_address, auto_sweep_enabled, updated_at
		 FROM treasury_config WHERE asset = $1`,
		asset,
	).Scan(&cfg.Asset, &sweepThreshold, &minBuffer, &cfg.ColdStorageAddress, &cfg.AutoSweepEnabled, &cfg.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrTreasuryConfigNotFound
		}
		return nil, fmt.Errorf("get treasury config: %w", err)
	}

	cfg.SweepThreshold, _ = decimal.NewFromString(sweepThreshold)
	cfg.MinOperatingBuffer, _ = decimal.NewFromString(minBuffer)
	return cfg, nil
}

func (r *TreasuryRepo) ListConfig(ctx context.Context) ([]*treasury.Config, error) {
	rows, err := r.db.Query(ctx,
		`SELECT asset, sweep_threshold, min_operating_buffer, cold_storage_address, auto_sweep_enabled, updated_at
		 FROM treasury_config ORDER BY asset ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list treasury config: %w", err)
	}
	defer rows.Close()

	var configs []*treasury.Config
	for rows.Next() {
		cfg := &treasury.Config{}
		var sweepThreshold, minBuffer string
		if err := rows.Scan(&cfg.Asset, &sweepThreshold, &minBuffer, &cfg.ColdStorageAddress, &cfg.AutoSweepEnabled, &cfg.UpdatedAt); err != nil {
			return nil, err
		}
		cfg.SweepThreshold, _ = decimal.NewFromString(sweepThreshold)
		cfg.MinOperatingBuffer, _ = decimal.NewFromString(minBuffer)
		configs = append(configs, cfg)
	}
	return configs, rows.Err()
}

func (r *TreasuryRepo) UpdateConfig(ctx context.Context, cfg *treasury.Config) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO treasury_config (asset, sweep_threshold, min_operating_buffer, cold_storage_address, auto_sweep_enabled, updated_at)
		 VALUES ($1, $2, $3, $4, $5, NOW())
		 ON CONFLICT (asset) DO UPDATE SET
		   sweep_threshold = EXCLUDED.sweep_threshold,
		   min_operating_buffer = EXCLUDED.min_operating_buffer,
		   cold_storage_address = EXCLUDED.cold_storage_address,
		   auto_sweep_enabled = EXCLUDED.auto_sweep_enabled,
		   updated_at = NOW()`,
		cfg.Asset, cfg.SweepThreshold.String(), cfg.MinOperatingBuffer.String(), cfg.ColdStorageAddress, cfg.AutoSweepEnabled,
	)
	if err != nil {
		return fmt.Errorf("update treasury config: %w", err)
	}
	return nil
}

// ListWalletPublicKeys returns every custodial wallet's public key across all
// tenants — treasury reserve accounting is a platform-wide concern, so this
// deliberately bypasses wallet.Repository's tenant scoping.
func (r *TreasuryRepo) ListWalletPublicKeys(ctx context.Context) ([]string, error) {
	rows, err := r.db.Query(ctx, `SELECT public_key FROM wallets`)
	if err != nil {
		return nil, fmt.Errorf("list wallet public keys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			return nil, err
		}
		keys = append(keys, pk)
	}
	return keys, rows.Err()
}

func (r *TreasuryRepo) RecordSweep(ctx context.Context, log *treasury.SweepLog) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO sweep_log (id, asset, amount, destination, tx_hash, triggered_by, swept_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		log.ID, log.Asset, log.Amount.String(), log.Destination, log.TxHash, log.TriggeredBy, log.SweptAt,
	)
	if err != nil {
		return fmt.Errorf("record sweep: %w", err)
	}
	return nil
}

func (r *TreasuryRepo) ListSweeps(ctx context.Context, limit, offset int) ([]*treasury.SweepLog, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, asset, amount, destination, tx_hash, triggered_by, swept_at
		 FROM sweep_log ORDER BY swept_at DESC LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list sweeps: %w", err)
	}
	defer rows.Close()

	var logs []*treasury.SweepLog
	for rows.Next() {
		entry := &treasury.SweepLog{}
		var amount string
		if err := rows.Scan(&entry.ID, &entry.Asset, &amount, &entry.Destination, &entry.TxHash, &entry.TriggeredBy, &entry.SweptAt); err != nil {
			return nil, err
		}
		entry.Amount, _ = decimal.NewFromString(amount)
		logs = append(logs, entry)
	}
	return logs, rows.Err()
}
