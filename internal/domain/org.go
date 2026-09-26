package domain

import "time"

const (
	RoleOwner     = "owner"
	RoleAdmin     = "admin"
	RoleDeveloper = "developer"
	RoleViewer    = "viewer"
)

type OrgMember struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	InvitedBy *string   `json:"invited_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	User      *User     `json:"user,omitempty"`
}

type OrgInvite struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Token     string    `json:"token"`
	Status    string    `json:"status"` // pending | accepted | expired
	InvitedBy *string   `json:"invited_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Membership audit actions.
const (
	OrgAuditRoleUpdated   = "role_updated"
	OrgAuditMemberRemoved = "member_removed"
)

// Membership audit outcomes. A refused attempt is recorded as deliberately as an
// applied one: a demotion or removal blocked by the last-owner guard is exactly
// the event an operator needs to be able to reconstruct afterwards.
const (
	OrgAuditOutcomeOK       = "ok"
	OrgAuditOutcomeRejected = "rejected"
	OrgAuditOutcomeNotFound = "not_found"
)

// MembershipAudit is one row of the membership audit trail: who tried what to
// whom, and how it ended. ActorUserID and TargetUserID are nullable and
// ON DELETE SET NULL, because the trail has to outlive the accounts it describes
// — and because the target of a missed removal is not a user of this tenant at
// all, so no user row exists to point at.
type MembershipAudit struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	ActorUserID  *string   `json:"actor_user_id,omitempty"`
	TargetUserID *string   `json:"target_user_id,omitempty"`
	Action       string    `json:"action"`
	PreviousRole string    `json:"previous_role,omitempty"`
	NewRole      string    `json:"new_role,omitempty"`
	Outcome      string    `json:"outcome"`
	CreatedAt    time.Time `json:"created_at"`
}
