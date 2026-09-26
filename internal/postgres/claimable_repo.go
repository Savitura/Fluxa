package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/claimable"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

type ClaimableBalanceRepo struct {
	db DB
}

func NewClaimableBalanceRepo(db DB) *ClaimableBalanceRepo {
	return &ClaimableBalanceRepo{db: db}
}

const claimableBalanceColumns = `id, org_id, asset, amount, claimants, sponsor, status, revoke_on_expiry, created_at, expires_at, claimed_at, claimed_by`

func (r *ClaimableBalanceRepo) Create(ctx context.Context, b *domain.ClaimableBalance) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		b.TenantID = &tID
	}

	claimants, err := json.Marshal(b.Claimants)
	if err != nil {
		return fmt.Errorf("encode claimable balance claimants: %w", err)
	}

	_, err = r.db.Exec(ctx,
		`INSERT INTO claimable_balances (`+claimableBalanceColumns+`)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		b.ID, nullableUUID(b.TenantID), b.Asset, b.Amount.String(), claimants, b.Sponsor,
		string(b.Status), b.RevokeOnExpiry, b.CreatedAt, nullableTime(b.ExpiresAt),
		nullableTime(b.ClaimedAt), b.ClaimedBy,
	)
	if err != nil {
		return fmt.Errorf("insert claimable balance: %w", err)
	}
	return nil
}

func (r *ClaimableBalanceRepo) GetByID(ctx context.Context, id string) (*domain.ClaimableBalance, error) {
	query := `SELECT ` + claimableBalanceColumns + ` FROM claimable_balances WHERE id = $1`
	args := []interface{}{id}
	if tID := tenant.IDFromContext(ctx); tID != "" {
		query += ` AND org_id = $2`
		args = append(args, tID)
	}

	balance, err := scanClaimableBalance(r.db.QueryRow(ctx, query, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrClaimableBalanceNotFound
		}
		return nil, fmt.Errorf("get claimable balance by id: %w", err)
	}
	return balance, nil
}

func (r *ClaimableBalanceRepo) List(ctx context.Context, f claimable.Filter) ([]*domain.ClaimableBalance, error) {
	conditions := []string{}
	args := []interface{}{}

	if tID := tenant.IDFromContext(ctx); tID != "" {
		args = append(args, tID)
		conditions = append(conditions, fmt.Sprintf("org_id = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Asset != "" {
		args = append(args, f.Asset)
		conditions = append(conditions, fmt.Sprintf("asset = $%d", len(args)))
	}
	if f.Claimant != "" {
		// Containment on the JSONB array so a claimant filter is an index
		// lookup rather than a scan of every row's claimants.
		probe, err := json.Marshal([]map[string]string{{"account": f.Claimant}})
		if err != nil {
			return nil, fmt.Errorf("encode claimant filter: %w", err)
		}
		args = append(args, string(probe))
		conditions = append(conditions, fmt.Sprintf("claimants @> $%d::jsonb", len(args)))
	}
	if f.ExpiresBefore != nil {
		args = append(args, *f.ExpiresBefore)
		conditions = append(conditions, fmt.Sprintf("expires_at < $%d", len(args)))
	}

	query := `SELECT ` + claimableBalanceColumns + ` FROM claimable_balances`
	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, " AND ")
	}

	args = append(args, f.Limit)
	query += fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d`, len(args))
	args = append(args, f.Offset)
	query += fmt.Sprintf(` OFFSET $%d`, len(args))

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list claimable balances: %w", err)
	}
	defer rows.Close()

	var balances []*domain.ClaimableBalance
	for rows.Next() {
		balance, err := scanClaimableBalance(rows)
		if err != nil {
			return nil, err
		}
		balances = append(balances, balance)
	}
	return balances, rows.Err()
}

func (r *ClaimableBalanceRepo) MarkClaimed(ctx context.Context, id, claimedBy string, at time.Time) error {
	return r.markTerminal(ctx, id, domain.ClaimableBalanceStatusClaimed, claimedBy, at)
}

func (r *ClaimableBalanceRepo) MarkExpired(ctx context.Context, id string, at time.Time) error {
	return r.markTerminal(ctx, id, domain.ClaimableBalanceStatusExpired, "", at)
}

func (r *ClaimableBalanceRepo) MarkRevoked(ctx context.Context, id string, at time.Time) error {
	return r.markTerminal(ctx, id, domain.ClaimableBalanceStatusRevoked, "", at)
}

// markTerminal moves a pending balance to a terminal status. claimed_at doubles
// as the terminal timestamp for every state; claimed_by is only set when a
// claimant actually took the funds.
//
// The status = 'pending' guard makes this a compare-and-set: a concurrent claim
// and expiry sweep cannot both win, so a balance can never be recorded as both
// claimed and expired.
func (r *ClaimableBalanceRepo) markTerminal(ctx context.Context, id string, status domain.ClaimableBalanceStatus, claimedBy string, at time.Time) error {
	query := `UPDATE claimable_balances SET status = $2, claimed_at = $3`
	args := []interface{}{id, string(status), at}

	if claimedBy != "" {
		args = append(args, claimedBy)
		query += fmt.Sprintf(`, claimed_by = $%d`, len(args))
	}

	query += ` WHERE id = $1 AND status = 'pending'`
	if tID := tenant.IDFromContext(ctx); tID != "" {
		args = append(args, tID)
		query += fmt.Sprintf(` AND org_id = $%d`, len(args))
	}

	_, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("mark claimable balance %s: %w", status, err)
	}
	return nil
}

// ListExpiredPending returns pending balances past their expiry across every
// tenant. The worker calls it with an unscoped context, so the org_id guard is
// deliberately absent — the expiry sweep is platform-wide.
func (r *ClaimableBalanceRepo) ListExpiredPending(ctx context.Context, now time.Time) ([]*domain.ClaimableBalance, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+claimableBalanceColumns+`
		 FROM claimable_balances
		 WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at < $1
		 ORDER BY expires_at ASC`,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("list expired claimable balances: %w", err)
	}
	defer rows.Close()

	var balances []*domain.ClaimableBalance
	for rows.Next() {
		balance, err := scanClaimableBalance(rows)
		if err != nil {
			return nil, err
		}
		balances = append(balances, balance)
	}
	return balances, rows.Err()
}

func scanClaimableBalance(row rowScanner) (*domain.ClaimableBalance, error) {
	b := &domain.ClaimableBalance{}
	var (
		amount    string
		claimants []byte
		status    string
	)

	if err := row.Scan(
		&b.ID, &b.TenantID, &b.Asset, &amount, &claimants, &b.Sponsor, &status,
		&b.RevokeOnExpiry, &b.CreatedAt, &b.ExpiresAt, &b.ClaimedAt, &b.ClaimedBy,
	); err != nil {
		return nil, err
	}

	b.Amount, _ = decimal.NewFromString(amount)
	b.Status = domain.ClaimableBalanceStatus(status)

	if len(claimants) > 0 {
		if err := json.Unmarshal(claimants, &b.Claimants); err != nil {
			return nil, fmt.Errorf("decode claimable balance claimants: %w", err)
		}
	}
	return b, nil
}
