package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type FeeRepo struct {
	db DB
}

func NewFeeRepo(db DB) *FeeRepo {
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
	// Both amount columns are nullable — the seeded default fee row leaves
	// max_fee_amount NULL to mean "no cap" — so they must be scanned into
	// pointers. Scanning NULL into a plain string fails every transfer with
	// a 500 at fee calculation.
	var minFee, maxFee *string

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
	if minFee != nil {
		schedule.MinFeeAmount, _ = decimal.NewFromString(*minFee)
	}
	if maxFee != nil && *maxFee != "" {
		maxVal, _ := decimal.NewFromString(*maxFee)
		schedule.MaxFeeAmount = &maxVal
	}

	return schedule, nil
}

func (r *FeeRepo) SetSchedule(ctx context.Context, schedule *domain.FeeSchedule) error {
	var maxFee interface{}
	if schedule.MaxFeeAmount != nil {
		maxFee = schedule.MaxFeeAmount.String()
	}

	_, err := r.db.Exec(ctx, `
		INSERT INTO fees (tenant_id, transfer_fee_bps, conversion_fee_bps, min_fee_amount, max_fee_amount, asset)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant_id, asset) WHERE tenant_id IS NOT NULL DO UPDATE SET
			transfer_fee_bps = EXCLUDED.transfer_fee_bps,
			conversion_fee_bps = EXCLUDED.conversion_fee_bps,
			min_fee_amount = EXCLUDED.min_fee_amount,
			max_fee_amount = EXCLUDED.max_fee_amount
	`, nullableUUID(schedule.TenantID), schedule.TransferFeeBps, schedule.ConversionFeeBps,
		schedule.MinFeeAmount.String(), maxFee, schedule.Asset)

	if err != nil {
		// Try the other conflict branch if tenant is null
		if schedule.TenantID == nil {
			_, err = r.db.Exec(ctx, `
				INSERT INTO fees (transfer_fee_bps, conversion_fee_bps, min_fee_amount, max_fee_amount, asset)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (asset) WHERE tenant_id IS NULL DO UPDATE SET
					transfer_fee_bps = EXCLUDED.transfer_fee_bps,
					conversion_fee_bps = EXCLUDED.conversion_fee_bps,
					min_fee_amount = EXCLUDED.min_fee_amount,
					max_fee_amount = EXCLUDED.max_fee_amount
			`, schedule.TransferFeeBps, schedule.ConversionFeeBps,
				schedule.MinFeeAmount.String(), maxFee, schedule.Asset)
		}
	}

	if err != nil {
		return fmt.Errorf("set fee schedule: %w", err)
	}
	return nil
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

func (r *FeeRepo) ListCollected(ctx context.Context, start, end *time.Time, tenantID *string, limit, offset int) ([]*domain.FeeCollection, error) {
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
		argN++
	}
	if tenantID != nil {
		query += fmt.Sprintf(" AND tenant_id = $%d", argN)
		args = append(args, *tenantID)
		argN++
	}

	query += " ORDER BY collected_at DESC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argN)
		args = append(args, limit)
		argN++
	}
	if offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argN)
		args = append(args, offset)
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

func (r *FeeRepo) GetMonthlyVolume(ctx context.Context, tenantID string) (decimal.Decimal, error) {
	var volume string
	err := r.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount), 0)
		FROM transactions
		WHERE tenant_id = $1 AND created_at >= NOW() - INTERVAL '30 days'
	`, tenantID).Scan(&volume)
	if err != nil {
		return decimal.Zero, err
	}
	vol, _ := decimal.NewFromString(volume)
	return vol, nil
}

func (r *FeeRepo) GetApplicableTier(ctx context.Context, tenantID string, volume decimal.Decimal) *domain.FeeTier {
	query := `
		SELECT id, tenant_id, min_volume, transfer_fee_bps, conversion_fee_bps
		FROM fee_tiers
		WHERE (tenant_id = $1 OR tenant_id IS NULL) AND min_volume <= $2
		ORDER BY min_volume DESC
		LIMIT 1
	`
	tier := &domain.FeeTier{}
	var tenantIDStr *string
	var minVol string
	err := r.db.QueryRow(ctx, query, tenantID, volume.String()).Scan(
		&tier.ID, &tenantIDStr, &minVol, &tier.TransferFeeBps, &tier.ConversionFeeBps,
	)
	if err != nil {
		return nil
	}
	tier.TenantID = tenantIDStr
	tier.MinVolume, _ = decimal.NewFromString(minVol)
	return tier
}
