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

type TransferApprovalRepo struct{ db DB }

func NewTransferApprovalRepo(db DB) *TransferApprovalRepo { return &TransferApprovalRepo{db: db} }

func (r *TransferApprovalRepo) GetPolicy(ctx context.Context, tenantID, asset string) (*domain.TransferApprovalPolicy, error) {
	var p domain.TransferApprovalPolicy
	var seconds float64
	err := r.db.QueryRow(ctx, `SELECT tenant_id::text, asset, threshold, required_approvals, EXTRACT(EPOCH FROM expires_after), enabled, updated_at FROM transfer_approval_policies WHERE tenant_id=$1 AND asset=$2`, tenantID, asset).Scan(&p.TenantID, &p.Asset, &p.Threshold, &p.RequiredApprovals, &seconds, &p.Enabled, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.ExpiresAfter = time.Duration(seconds * float64(time.Second))
	return &p, nil
}

func (r *TransferApprovalRepo) PutPolicy(ctx context.Context, p *domain.TransferApprovalPolicy) error {
	_, err := TxFromContext(ctx, r.db).Exec(ctx, `INSERT INTO transfer_approval_policies (tenant_id,asset,threshold,required_approvals,expires_after,enabled) VALUES ($1,$2,$3,$4,$5 * INTERVAL '1 second',$6) ON CONFLICT (tenant_id,asset) DO UPDATE SET threshold=EXCLUDED.threshold,required_approvals=EXCLUDED.required_approvals,expires_after=EXCLUDED.expires_after,enabled=EXCLUDED.enabled,updated_at=NOW()`, p.TenantID, p.Asset, p.Threshold, p.RequiredApprovals, int64(p.ExpiresAfter/time.Second), p.Enabled)
	return err
}

func (r *TransferApprovalRepo) CreateRequest(ctx context.Context, req *domain.TransferApprovalRequest) error {
	_, err := TxFromContext(ctx, r.db).Exec(ctx, `INSERT INTO transfer_approval_requests (id,tenant_id,transaction_id,creator_id,creator_api_key_id,asset,amount,required_approvals,status,expires_at,created_at) VALUES ($1,$2,$3,NULLIF($4,'')::uuid,NULLIF($5,'')::uuid,$6,$7,$8,$9,$10,$11)`, req.ID, req.TenantID, req.TransactionID, req.CreatorID, req.CreatorAPIKeyID, req.Asset, req.Amount, req.RequiredApprovals, req.Status, req.ExpiresAt, req.CreatedAt)
	return err
}

func (r *TransferApprovalRepo) ListRequests(ctx context.Context, tenantID, status string, limit, offset int) ([]*domain.TransferApprovalRequest, int, error) {
	if status == "" {
		status = "all"
	}
	// Expiration is enforced on reads as well as votes so abandoned approvals
	// cannot remain indefinitely pending.
	err := RunInTx(ctx, r.db, func(txCtx context.Context) error {
		db := TxFromContext(txCtx, r.db)
		if _, err := db.Exec(txCtx, `UPDATE transactions t SET status='failed',failure_reason='approval expired' FROM transfer_approval_requests ar WHERE ar.transaction_id=t.id AND ar.tenant_id=$1 AND ar.status='pending' AND ar.expires_at<=NOW() AND t.status='approval_pending'`, tenantID); err != nil {
			return err
		}
		_, err := db.Exec(txCtx, `UPDATE transfer_approval_requests SET status='expired',decided_at=NOW() WHERE tenant_id=$1 AND status='pending' AND expires_at<=NOW()`, tenantID)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM transfer_approval_requests WHERE tenant_id=$1 AND ($2='all' OR status=$2)`, tenantID, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.Query(ctx, `SELECT id::text,tenant_id::text,transaction_id::text,COALESCE(creator_id::text,''),COALESCE(creator_api_key_id::text,''),asset,amount,required_approvals,status,expires_at,created_at,decided_at,(SELECT COUNT(*) FROM transfer_approval_votes v WHERE v.request_id=r.id AND v.decision='approved') FROM transfer_approval_requests r WHERE tenant_id=$1 AND ($2='all' OR status=$2) ORDER BY created_at DESC LIMIT $3 OFFSET $4`, tenantID, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var result []*domain.TransferApprovalRequest
	for rows.Next() {
		var q domain.TransferApprovalRequest
		if err := rows.Scan(&q.ID, &q.TenantID, &q.TransactionID, &q.CreatorID, &q.CreatorAPIKeyID, &q.Asset, &q.Amount, &q.RequiredApprovals, &q.Status, &q.ExpiresAt, &q.CreatedAt, &q.DecidedAt, &q.ApprovalCount); err != nil {
			return nil, 0, err
		}
		votes, err := r.db.Query(ctx, `SELECT id::text,actor_id::text,decision,note,created_at FROM transfer_approval_votes WHERE request_id=$1 ORDER BY created_at`, q.ID)
		if err != nil {
			return nil, 0, err
		}
		q.Votes = []domain.TransferApprovalVote{}
		for votes.Next() {
			var v domain.TransferApprovalVote
			if err := votes.Scan(&v.ID, &v.ActorID, &v.Decision, &v.Note, &v.CreatedAt); err != nil {
				votes.Close()
				return nil, 0, err
			}
			q.Votes = append(q.Votes, v)
		}
		votes.Close()
		result = append(result, &q)
	}
	return result, total, rows.Err()
}

func (r *TransferApprovalRepo) CastVote(ctx context.Context, tenantID, requestID, actorID, decision, note string) (*domain.TransferApprovalRequest, bool, error) {
	var out domain.TransferApprovalRequest
	release := false
	expired := false
	err := RunInTx(ctx, r.db, func(txctx context.Context) error {
		tx := TxFromContext(txctx, r.db)
		var creator string
		var required int
		var status string
		var expires time.Time
		err := tx.QueryRow(txctx, `SELECT transaction_id::text,COALESCE(creator_id::text,''),required_approvals,status,expires_at FROM transfer_approval_requests WHERE id=$1 AND tenant_id=$2 FOR UPDATE`, requestID, tenantID).Scan(&out.TransactionID, &creator, &required, &status, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrTransferApprovalNotFound
		}
		if err != nil {
			return err
		}
		if status != "pending" {
			return domain.ErrApprovalAlreadyDecided
		}
		if !expires.After(time.Now()) {
			_, err = tx.Exec(txctx, `UPDATE transfer_approval_requests SET status='expired',decided_at=NOW() WHERE id=$1`, requestID)
			if err != nil {
				return err
			}
			_, err = tx.Exec(txctx, `UPDATE transactions SET status='failed',failure_reason='approval expired' WHERE id=$1 AND tenant_id=$2 AND status='approval_pending'`, out.TransactionID, tenantID)
			if err != nil {
				return err
			}
			expired = true
			return nil
		}
		if err := checkSelfApproval(creator, actorID, decision); err != nil {
			return err
		}
		_, err = tx.Exec(txctx, `INSERT INTO transfer_approval_votes (request_id,tenant_id,actor_id,decision,note) VALUES ($1,$2,$3,$4,$5)`, requestID, tenantID, actorID, decision, note)
		if err != nil {
			return fmt.Errorf("record approval vote: %w", err)
		}
		var count int
		if err = tx.QueryRow(txctx, `SELECT COUNT(*) FROM transfer_approval_votes WHERE request_id=$1 AND decision='approved'`, requestID).Scan(&count); err != nil {
			return err
		}
		newStatus := status
		if decision == "rejected" {
			newStatus = "rejected"
			_, err = tx.Exec(txctx, `UPDATE transactions SET status='failed',failure_reason='transfer approval rejected' WHERE id=$1 AND tenant_id=$2 AND status='approval_pending'`, out.TransactionID, tenantID)
		} else if count >= required {
			newStatus = "approved"
			release = true
			_, err = tx.Exec(txctx, `UPDATE transactions SET status='pending' WHERE id=$1 AND tenant_id=$2 AND status='approval_pending'`, out.TransactionID, tenantID)
		}
		if err != nil {
			return err
		}
		if newStatus != "pending" {
			_, err = tx.Exec(txctx, `UPDATE transfer_approval_requests SET status=$2,decided_at=NOW() WHERE id=$1`, requestID, newStatus)
			if err != nil {
				return err
			}
		}
		return tx.QueryRow(txctx, `SELECT id::text,tenant_id::text,transaction_id::text,COALESCE(creator_id::text,''),COALESCE(creator_api_key_id::text,''),asset,amount,required_approvals,status,expires_at,created_at,decided_at,(SELECT COUNT(*) FROM transfer_approval_votes v WHERE v.request_id=r.id AND v.decision='approved') FROM transfer_approval_requests r WHERE id=$1`, requestID).Scan(&out.ID, &out.TenantID, &out.TransactionID, &out.CreatorID, &out.CreatorAPIKeyID, &out.Asset, &out.Amount, &out.RequiredApprovals, &out.Status, &out.ExpiresAt, &out.CreatedAt, &out.DecidedAt, &out.ApprovalCount)
	})
	if err != nil {
		return nil, false, err
	}
	if expired {
		return nil, false, domain.ErrApprovalExpired
	}
	return &out, release, nil
}

// ExpirePending expires up to limit pending requests past expires_at across all
// tenants and fails their approval_pending transactions, in one statement so a
// request is never expired without its transaction being failed. SKIP LOCKED
// lets the sweep run alongside CastVote (which locks the row it decides) and
// alongside another worker replica without blocking either. The FOR UPDATE
// also keeps ReplicaAwareDB from routing this WITH statement to a replica.
func (r *TransferApprovalRepo) ExpirePending(ctx context.Context, limit int) ([]domain.ExpiredTransferApproval, error) {
	rows, err := r.db.Query(ctx, `
		WITH due AS (
			SELECT id FROM transfer_approval_requests
			WHERE status = 'pending' AND expires_at <= NOW()
			ORDER BY expires_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		), expired AS (
			UPDATE transfer_approval_requests ar SET status = 'expired', decided_at = NOW()
			FROM due WHERE ar.id = due.id
			RETURNING ar.id, ar.tenant_id, ar.transaction_id, ar.asset, ar.amount
		), failed AS (
			UPDATE transactions t SET status = 'failed', failure_reason = 'approval expired'
			FROM expired e
			WHERE t.id = e.transaction_id AND t.tenant_id = e.tenant_id AND t.status = 'approval_pending'
			RETURNING t.id
		)
		SELECT e.id::text, e.tenant_id::text, e.transaction_id::text, e.asset, e.amount::text, f.id IS NOT NULL
		FROM expired e LEFT JOIN failed f ON f.id = e.transaction_id`, limit)
	if err != nil {
		return nil, fmt.Errorf("expire pending transfer approvals: %w", err)
	}
	defer rows.Close()
	var out []domain.ExpiredTransferApproval
	for rows.Next() {
		var e domain.ExpiredTransferApproval
		var amount string
		if err := rows.Scan(&e.RequestID, &e.TenantID, &e.TransactionID, &e.Asset, &amount, &e.TransactionFailed); err != nil {
			return nil, err
		}
		if e.Amount, err = decimal.NewFromString(amount); err != nil {
			return nil, fmt.Errorf("parse expired approval amount: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// checkSelfApproval enforces the four-eyes rule: the user who created a
// transfer can never approve it, whatever the policy's required count. They
// may still reject (cancel) it. API-key-created requests have no creator user
// (creator_id is NULL, creator_api_key_id is set) and votes always come from
// users, so no voter can match them.
func checkSelfApproval(creatorUserID, actorID, decision string) error {
	if decision == "approved" && creatorUserID != "" && creatorUserID == actorID {
		return domain.ErrApprovalCreatorCannotApprove
	}
	return nil
}
