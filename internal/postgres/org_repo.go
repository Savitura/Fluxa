package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrgRepo struct {
	db *pgxpool.Pool
}

func NewOrgRepo(db *pgxpool.Pool) *OrgRepo {
	return &OrgRepo{db: db}
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

func (r *OrgRepo) UpdateMemberRole(ctx context.Context, tenantID, userID, newRole string) error {
	res, err := r.db.Exec(ctx,
		`UPDATE organization_members SET role = $1 WHERE tenant_id = $2 AND user_id = $3`,
		newRole, tenantID, userID,
	)
	if err != nil {
		return fmt.Errorf("update member role: %w", err)
	}
	if res.RowsAffected() == 0 {
		return domain.ErrOrgMemberNotFound
	}
	return nil
}

func (r *OrgRepo) RemoveMember(ctx context.Context, tenantID, userID string) error {
	res, err := r.db.Exec(ctx,
		`DELETE FROM organization_members WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID,
	)
	if err != nil {
		return fmt.Errorf("remove org member: %w", err)
	}
	if res.RowsAffected() == 0 {
		return domain.ErrOrgMemberNotFound
	}
	return nil
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
