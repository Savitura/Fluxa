package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
)

type AnchorRepo struct {
	db DB
}

func NewAnchorRepo(db DB) *AnchorRepo {
	return &AnchorRepo{db: db}
}

func (r *AnchorRepo) CreateAnchor(ctx context.Context, a *domain.Anchor) error {
	assetsJSON, err := json.Marshal(a.SupportedAssets)
	if err != nil {
		return fmt.Errorf("marshal supported assets: %w", err)
	}
	sepVersionsJSON, err := json.Marshal(a.SepVersions)
	if err != nil {
		return fmt.Errorf("marshal sep versions: %w", err)
	}

	query := `
		INSERT INTO anchors (id, home_domain, transfer_server, transfer_server_sep24, web_auth_endpoint, sep10_signing_key, network_passphrase, supported_assets, sep_versions, registered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err = r.db.Exec(ctx, query,
		a.ID, a.HomeDomain, a.TransferServer, a.TransferServerSep24, a.WebAuthEndpoint,
		a.Sep10SigningKey, a.NetworkPassphrase, assetsJSON, sepVersionsJSON, a.RegisteredAt,
	)
	if err != nil {
		return fmt.Errorf("insert anchor: %w", err)
	}
	return nil
}

func (r *AnchorRepo) ListAnchors(ctx context.Context) ([]*domain.Anchor, error) {
	query := `
		SELECT id, home_domain, transfer_server, transfer_server_sep24, web_auth_endpoint, sep10_signing_key, network_passphrase, supported_assets, sep_versions, registered_at
		FROM anchors ORDER BY registered_at ASC
	`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list anchors: %w", err)
	}
	defer rows.Close()

	var anchors []*domain.Anchor
	for rows.Next() {
		a, err := scanAnchor(rows)
		if err != nil {
			return nil, err
		}
		anchors = append(anchors, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate anchors: %w", err)
	}
	return anchors, nil
}

func (r *AnchorRepo) GetAnchorByID(ctx context.Context, id string) (*domain.Anchor, error) {
	query := `
		SELECT id, home_domain, transfer_server, transfer_server_sep24, web_auth_endpoint, sep10_signing_key, network_passphrase, supported_assets, sep_versions, registered_at
		FROM anchors WHERE id = $1
	`
	a, err := scanAnchor(r.db.QueryRow(ctx, query, id))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("anchor not found")
		}
		return nil, err
	}
	return a, nil
}

func (r *AnchorRepo) GetAnchorByHomeDomain(ctx context.Context, homeDomain string) (*domain.Anchor, error) {
	query := `
		SELECT id, home_domain, transfer_server, transfer_server_sep24, web_auth_endpoint, sep10_signing_key, network_passphrase, supported_assets, sep_versions, registered_at
		FROM anchors WHERE home_domain = $1
	`
	a, err := scanAnchor(r.db.QueryRow(ctx, query, homeDomain))
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("anchor not found")
		}
		return nil, err
	}
	return a, nil
}

// anchorRowScanner is satisfied by both pgx.Row and pgx.Rows.
type anchorRowScanner interface {
	Scan(dest ...interface{}) error
}

func scanAnchor(row anchorRowScanner) (*domain.Anchor, error) {
	var a domain.Anchor
	var assetsJSON, sepVersionsJSON []byte
	err := row.Scan(
		&a.ID, &a.HomeDomain, &a.TransferServer, &a.TransferServerSep24, &a.WebAuthEndpoint,
		&a.Sep10SigningKey, &a.NetworkPassphrase, &assetsJSON, &sepVersionsJSON, &a.RegisteredAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(assetsJSON, &a.SupportedAssets); err != nil {
		return nil, fmt.Errorf("unmarshal supported assets: %w", err)
	}
	if err := json.Unmarshal(sepVersionsJSON, &a.SepVersions); err != nil {
		return nil, fmt.Errorf("unmarshal sep versions: %w", err)
	}
	return &a, nil
}

func (r *AnchorRepo) CreateTransaction(ctx context.Context, t *domain.AnchorTransaction) error {
	query := `
		INSERT INTO anchor_transactions (id, user_id, wallet_id, anchor_id, external_tx_id, asset, amount, type, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.Exec(ctx, query,
		t.ID, nullableUUID(t.UserID), t.WalletID, t.AnchorID, t.ExternalTxID,
		t.Asset, t.Amount, t.Type, t.Status, t.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert anchor transaction: %w", err)
	}
	return nil
}

func (r *AnchorRepo) GetTransactionByID(ctx context.Context, id string, tenantID *string) (*domain.AnchorTransaction, error) {
	query := `
		SELECT t.id, t.user_id, t.wallet_id, t.anchor_id, t.external_tx_id, t.asset, t.amount, t.type, t.status, t.created_at, t.completed_at
		FROM anchor_transactions t
	`
	args := []interface{}{id}
	if tenantID != nil {
		query += ` JOIN wallets w ON w.id = t.wallet_id WHERE t.id = $1 AND w.tenant_id = $2`
		args = append(args, *tenantID)
	} else {
		query += ` WHERE t.id = $1`
	}

	var t domain.AnchorTransaction
	err := r.db.QueryRow(ctx, query, args...).Scan(
		&t.ID, &t.UserID, &t.WalletID, &t.AnchorID, &t.ExternalTxID,
		&t.Asset, &t.Amount, &t.Type, &t.Status, &t.CreatedAt, &t.CompletedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("anchor transaction not found")
		}
		return nil, err
	}
	return &t, nil
}

func (r *AnchorRepo) UpdateTransactionStatus(ctx context.Context, id, status string, completedAt *time.Time, tenantID *string) error {
	query := `UPDATE anchor_transactions SET status = $1, completed_at = $2 WHERE id = $3`
	args := []interface{}{status, completedAt, id}

	if tenantID != nil {
		// Need to enforce tenant ownership during update.
		// Since we cannot easily join in an UPDATE without FROM, we can use a subquery in WHERE.
		query += ` AND wallet_id IN (SELECT id FROM wallets WHERE tenant_id = $4)`
		args = append(args, *tenantID)
	}

	_, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update anchor transaction status: %w", err)
	}
	return nil
}
