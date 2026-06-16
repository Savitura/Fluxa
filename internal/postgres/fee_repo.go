package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type FeeRepo struct {
	db *pgxpool.Pool
}

func NewFeeRepo(db *pgxpool.Pool) *FeeRepo {
	return &FeeRepo{db: db}
}

func (r *FeeRepo) GetSchedule(ctx context.Context, tenantID *string, asset string) (*domain.FeeSchedule, error) {
	if tenantID != nil {
		schedule, err := r.getScheduleForTenant(ctx, tenantID, asset)
		if err == nil {
			return schedule, nil
		}
		if !errors.Is(err, domain.ErrFeeScheduleNotFound) {
			return nil, err
		}
	}

	schedule, err := r.getScheduleForTenant(ctx, nil, asset)
	if err == nil {
		return schedule, nil
	}
	if !errors.Is(err, domain.ErrFeeScheduleNotFound) || asset == "*" {
		return nil, err
	}

	return r.getScheduleForTenant(ctx, nil, "*")
}

func (r *FeeRepo) getScheduleForTenant(ctx context.Context, tenantID *string, asset string) (*domain.FeeSchedule, error) {
	schedule := &domain.FeeSchedule{}
	var tenantIDStr *string
	var minFee, maxFee string

	query := `
		SELECT id, tenant_id, transfer_fee_bps, conversion_fee_bps,
		       min_fee_amount, max_fee_amount, asset, created_at
		FROM fees
		WHERE asset = $1`

	args := []interface{}{asset}
	if tenantID != nil {
		query += " AND tenant_id = $2"
		args = append(args, *tenantID)
	} else {
		query += " AND tenant_id IS NULL"
	}

	err := r.db.QueryRow(ctx, query, args...).Scan(
		&schedule.ID, &tenantIDStr, &schedule.TransferFeeBps, &schedule.ConversionFeeBps,
		&minFee, &maxFee, &schedule.Asset, &schedule.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrFeeScheduleNotFound
		}
		return nil, fmt.Errorf("get fee schedule: %w", err)
	}

	schedule.TenantID = tenantIDStr
	schedule.MinFeeAmount, _ = decimal.NewFromString(minFee)
	if maxFee != "" {
		maxVal, _ := decimal.NewFromString(maxFee)
		schedule.MaxFeeAmount = &maxVal
	}

	return schedule, nil
}

func (r *FeeRepo) RecordCollection(ctx context.Context, collection *domain.FeeCollection) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO fee_collections (id, transaction_id, tenant_id, fee_amount, asset, fee_bps, collected_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		collection.ID, collection.TransactionID, nullableUUID(collection.TenantID),
		collection.FeeAmount.String(), collection.Asset, collection.FeeBps, collection.CollectedAt,
	)
	if err != nil {
		return fmt.Errorf("insert fee collection: %w", err)
	}
	return nil
}

func (r *FeeRepo) ListCollected(ctx context.Context, start, end *time.Time) ([]*domain.FeeCollection, error) {
	query := `
		SELECT id, transaction_id, tenant_id, fee_amount, asset, fee_bps, collected_at
		FROM fee_collections
		WHERE 1=1`
	args := []interface{}{}
	argN := 1

	if start != nil {
		query += fmt.Sprintf(" AND collected_at >= $%d", argN)
		args = append(args, *start)
		argN++
	}
	if end != nil {
		query += fmt.Sprintf(" AND collected_at <= $%d", argN)
		args = append(args, *end)
	}

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list fee collections: %w", err)
	}
	defer rows.Close()

	var collections []*domain.FeeCollection
	for rows.Next() {
		c := &domain.FeeCollection{}
		var tenantID *string
		var feeAmount string
		if err := rows.Scan(&c.ID, &c.TransactionID, &tenantID, &feeAmount, &c.Asset, &c.FeeBps, &c.CollectedAt); err != nil {
			return nil, err
		}
		c.TenantID = tenantID
		c.FeeAmount, _ = decimal.NewFromString(feeAmount)
		collections = append(collections, c)
	}
	return collections, rows.Err()
}
