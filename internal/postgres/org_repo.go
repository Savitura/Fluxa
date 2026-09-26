package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type OrgRepo struct {
	db DB
	// tx adapts db so a membership change can run inside a transaction. It is a
	// field rather than a direct call to db.Begin so the last-owner guard can be
	// exercised against a recording stub: the package has no test double for
	// pgx.Tx and no mocking dependency to lean on.
	tx txPool
}

func NewOrgRepo(db DB) *OrgRepo {
	return &OrgRepo{db: db, tx: poolTx{db: db}}
}

// dbTx is the transaction surface the last-owner guard uses. It is deliberately
// narrower than pgx.Tx: poolTx and pgxTxAdapter adapt the real handles to it,
// and tests supply a stub, so the guard's locking, refusal and audit behaviour
// are all checkable without a live database.
type dbTx interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
	Begin(context.Context) (dbTx, error)
}

// txPool can open a transaction.
type txPool interface {
	Begin(context.Context) (dbTx, error)
}

// poolTx adapts the repository's DB handle to dbTx.
type poolTx struct {
	db DB
}

func (p poolTx) Begin(ctx context.Context) (dbTx, error) {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgxTxAdapter{Tx: tx}, nil
}

// pgxTxAdapter narrows a real pgx.Tx to dbTx, so a guard can join an enclosing
// transaction instead of opening a second connection.
type pgxTxAdapter struct {
	pgx.Tx
}

func (t pgxTxAdapter) Begin(ctx context.Context) (dbTx, error) {
	tx, err := t.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgxTxAdapter{Tx: tx}, nil
}

func (r *OrgRepo) AddMember(ctx context.Context, m *domain.OrgMember) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO organization_members (id, tenant_id, user_id, role, invited_by, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		m.ID, m.TenantID, m.UserID, m.Role, nullableUUID(m.InvitedBy), m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("add org member: %w", err)
	}
	return nil
}

func (r *OrgRepo) GetMember(ctx context.Context, tenantID, userID string) (*domain.OrgMember, error) {
	m := &domain.OrgMember{}
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, user_id, role, invited_by, created_at
		 FROM organization_members
		 WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	).Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.InvitedBy, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrgMemberNotFound
		}
		return nil, fmt.Errorf("get member: %w", err)
	}
	return m, nil
}

func (r *OrgRepo) GetUserActiveMember(ctx context.Context, userID string) (*domain.OrgMember, error) {
	m := &domain.OrgMember{}
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, user_id, role, invited_by, created_at
		 FROM organization_members
		 WHERE user_id = $1
		 ORDER BY created_at ASC LIMIT 1`,
		userID,
	).Scan(&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.InvitedBy, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrgMemberNotFound
		}
		return nil, fmt.Errorf("get user active member: %w", err)
	}
	return m, nil
}

func (r *OrgRepo) ListMembers(ctx context.Context, tenantID string) ([]*domain.OrgMember, error) {
	rows, err := r.db.Query(ctx,
		`SELECT m.id, m.tenant_id, m.user_id, m.role, m.invited_by, m.created_at,
		        u.id, u.email, u.name, u.created_at
		 FROM organization_members m
		 JOIN users u ON m.user_id = u.id
		 WHERE m.tenant_id = $1
		 ORDER BY m.created_at ASC`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list org members: %w", err)
	}
	defer rows.Close()

	var members []*domain.OrgMember
	for rows.Next() {
		m := &domain.OrgMember{}
		u := &domain.User{}
		err := rows.Scan(
			&m.ID, &m.TenantID, &m.UserID, &m.Role, &m.InvitedBy, &m.CreatedAt,
			&u.ID, &u.Email, &u.Name, &u.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan org member: %w", err)
		}
		m.User = u
		members = append(members, m)
	}
	return members, rows.Err()
}

// evaluateOwnerGuard is the last-owner invariant as a pure function.
//
// A role change or removal is refused for exactly one reason: it would leave the
// tenant with no owner, and therefore no administrator. Everything else is
// allowed, so a tenant that already has two owners can always demote or remove
// one of them, and promoting a member is never blocked — that promotion is the
// documented way out of the guard.
//
// The tenant row is locked for the whole change (see lockTenantForUpdate), so
// this decision is made against an owner set no concurrent request can mutate.
// It therefore needs no second read of its own to stay correct.
//
// A tenant that somehow has zero owners refuses removals until an owner is
// promoted: recovering from that state requires a promotion, which always
// increases the owner count and is never refused.
func evaluateOwnerGuard(owners int, targetIsOwner bool, keepsOwnership bool) error {
	if targetIsOwner && !keepsOwnership && owners <= 1 {
		return domain.ErrLastOrgOwner
	}
	return nil
}

// lockTenantForUpdate takes a row lock on the tenant and returns how many owners
// it currently has.
//
// Locking the single tenant row — rather than the owner rows — gives every
// membership mutation one lock acquisition point in a fixed order, so two
// concurrent demotions of the same tenant serialize instead of deadlocking on
// overlapping owner rows. Under READ COMMITTED the second transaction observes
// the owner count the first one committed, so it cannot conclude that an owner
// remains when that owner is already gone.
func lockTenantForUpdate(ctx context.Context, tx dbTx, tenantID string) (int, error) {
	var lockedTenantID string
	err := tx.QueryRow(ctx, `SELECT id FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&lockedTenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, domain.ErrOrgNotFound
		}
		return 0, fmt.Errorf("lock tenant: %w", err)
	}

	var owners int
	err = tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM organization_members WHERE tenant_id = $1 AND role = $2`,
		tenantID, domain.RoleOwner,
	).Scan(&owners)
	if err != nil {
		return 0, fmt.Errorf("count tenant owners: %w", err)
	}
	return owners, nil
}

// currentMemberRole reads the target's role inside the guard's transaction, so
// the decision and the mutation observe the same snapshot. The lookup is scoped
// by tenant: a user who belongs to some other organization is simply not a member
// here, and is reported as not found rather than acted on.
func currentMemberRole(ctx context.Context, tx dbTx, tenantID, userID string) (string, error) {
	var role string
	err := tx.QueryRow(ctx,
		`SELECT role FROM organization_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrOrgMemberNotFound
		}
		return "", fmt.Errorf("read member role: %w", err)
	}
	return role, nil
}

// recordMembershipAudit appends one entry to the membership audit trail. A
// refused attempt is recorded as deliberately as an applied one: reconstructing
// "who tried to strip the last owner" is the reason the trail exists.
func recordMembershipAudit(ctx context.Context, tx dbTx, e domain.MembershipAudit) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO organization_member_audit
		   (tenant_id, actor_user_id, target_user_id, action, previous_role, new_role, outcome)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		e.TenantID,
		nullableUUID(e.ActorUserID),
		nullableUUID(e.TargetUserID),
		e.Action, e.PreviousRole, e.NewRole, e.Outcome,
	)
	if err != nil {
		return fmt.Errorf("record membership audit: %w", err)
	}
	return nil
}

// commitAuditOnly records an attempt that was refused or missed, then commits —
// so the audit row survives while the membership change itself is never applied.
// It returns refuse, the error the caller should surface, only once the audit
// row is durable: a failed write must not be masked by the refusal it records.
func commitAuditOnly(ctx context.Context, tx dbTx, e domain.MembershipAudit, refuse error) error {
	if err := recordMembershipAudit(ctx, tx, e); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit membership audit: %w", err)
	}
	return refuse
}

// commitMembershipChange writes the audit entry alongside the change it records
// and commits them together, so a trail can never claim a change that was rolled
// back.
func commitMembershipChange(ctx context.Context, tx dbTx, e domain.MembershipAudit) error {
	if err := recordMembershipAudit(ctx, tx, e); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit membership change: %w", err)
	}
	return nil
}

// UpdateMemberRole changes a member's role, refusing to leave the tenant without
// an owner.
//
// The check and the mutation run in one transaction under the tenant row lock, so
// two concurrent demotions of the last two owners cannot both observe two owners
// and both succeed: the second blocks on the lock, re-reads the count the first
// committed, and is refused with domain.ErrLastOrgOwner. Exactly one membership
// row is written on success, and none at all on refusal.
func (r *OrgRepo) UpdateMemberRole(ctx context.Context, tenantID string, actorUserID, userID, newRole string) error {
	tx, err := r.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin member role transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once Commit has run

	owners, err := lockTenantForUpdate(ctx, tx, tenantID)
	if err != nil {
		return err
	}

	currentRole, err := currentMemberRole(ctx, tx, tenantID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrOrgMemberNotFound) {
			return commitAuditOnly(ctx, tx, domain.MembershipAudit{
				TenantID:    tenantID,
				ActorUserID: &actorUserID,
				Action:      domain.OrgAuditRoleUpdated,
				NewRole:     newRole,
				Outcome:     domain.OrgAuditOutcomeNotFound,
			}, domain.ErrOrgMemberNotFound)
		}
		return err
	}

	audit := domain.MembershipAudit{
		TenantID:     tenantID,
		ActorUserID:  &actorUserID,
		TargetUserID: &userID,
		Action:       domain.OrgAuditRoleUpdated,
		PreviousRole: currentRole,
		NewRole:      newRole,
	}

	if err := evaluateOwnerGuard(owners, currentRole == domain.RoleOwner, newRole == domain.RoleOwner); err != nil {
		audit.Outcome = domain.OrgAuditOutcomeRejected
		return commitAuditOnly(ctx, tx, audit, err)
	}

	res, err := tx.Exec(ctx,
		`UPDATE organization_members SET role = $1 WHERE tenant_id = $2 AND user_id = $3`,
		newRole, tenantID, userID,
	)
	if err != nil {
		return fmt.Errorf("update member role: %w", err)
	}
	if res.RowsAffected() == 0 {
		audit.Outcome = domain.OrgAuditOutcomeNotFound
		return commitAuditOnly(ctx, tx, audit, domain.ErrOrgMemberNotFound)
	}

	audit.Outcome = domain.OrgAuditOutcomeOK
	return commitMembershipChange(ctx, tx, audit)
}

// RemoveMember removes a member, refusing to remove the tenant's last owner. It
// shares UpdateMemberRole's lock, guard and audit treatment, so the last owner
// has to be demoted — or a second owner promoted — before they can be removed.
func (r *OrgRepo) RemoveMember(ctx context.Context, tenantID string, actorUserID, userID string) error {
	tx, err := r.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin remove member transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once Commit has run

	owners, err := lockTenantForUpdate(ctx, tx, tenantID)
	if err != nil {
		return err
	}

	currentRole, err := currentMemberRole(ctx, tx, tenantID, userID)
	if err != nil {
		if errors.Is(err, domain.ErrOrgMemberNotFound) {
			return commitAuditOnly(ctx, tx, domain.MembershipAudit{
				TenantID:    tenantID,
				ActorUserID: &actorUserID,
				Action:      domain.OrgAuditMemberRemoved,
				Outcome:     domain.OrgAuditOutcomeNotFound,
			}, domain.ErrOrgMemberNotFound)
		}
		return err
	}

	audit := domain.MembershipAudit{
		TenantID:     tenantID,
		ActorUserID:  &actorUserID,
		TargetUserID: &userID,
		Action:       domain.OrgAuditMemberRemoved,
		PreviousRole: currentRole,
	}

	if err := evaluateOwnerGuard(owners, currentRole == domain.RoleOwner, false); err != nil {
		audit.Outcome = domain.OrgAuditOutcomeRejected
		return commitAuditOnly(ctx, tx, audit, err)
	}

	res, err := tx.Exec(ctx,
		`DELETE FROM organization_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	)
	if err != nil {
		return fmt.Errorf("remove org member: %w", err)
	}
	if res.RowsAffected() == 0 {
		audit.Outcome = domain.OrgAuditOutcomeNotFound
		return commitAuditOnly(ctx, tx, audit, domain.ErrOrgMemberNotFound)
	}

	audit.Outcome = domain.OrgAuditOutcomeOK
	return commitMembershipChange(ctx, tx, audit)
}

func (r *OrgRepo) CreateInvite(ctx context.Context, inv *domain.OrgInvite) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO organization_invites (id, tenant_id, email, role, token, status, invited_by, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		inv.ID, inv.TenantID, inv.Email, inv.Role, inv.Token, inv.Status, nullableUUID(inv.InvitedBy), inv.CreatedAt, inv.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("create org invite: %w", err)
	}
	return nil
}

func (r *OrgRepo) GetInviteByToken(ctx context.Context, token string) (*domain.OrgInvite, error) {
	inv := &domain.OrgInvite{}
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, email, role, token, status, invited_by, created_at, expires_at
		 FROM organization_invites WHERE token = $1`,
		token,
	).Scan(&inv.ID, &inv.TenantID, &inv.Email, &inv.Role, &inv.Token, &inv.Status, &inv.InvitedBy, &inv.CreatedAt, &inv.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrInviteNotFound
		}
		return nil, fmt.Errorf("get invite by token: %w", err)
	}
	return inv, nil
}

func (r *OrgRepo) UpdateInviteStatus(ctx context.Context, inviteID, status string) error {
	_, err := r.db.Exec(ctx,
		`UPDATE organization_invites SET status = $1 WHERE id = $2`,
		status, inviteID,
	)
	return err
}
