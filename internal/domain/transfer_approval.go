package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type TransferApprovalPolicy struct {
	TenantID          string          `json:"tenant_id"`
	Asset             string          `json:"asset"`
	Threshold         decimal.Decimal `json:"threshold"`
	RequiredApprovals int             `json:"required_approvals"`
	ExpiresAfter      time.Duration   `json:"expires_after_seconds"`
	Enabled           bool            `json:"enabled"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type TransferApprovalVote struct {
	ID        string    `json:"id"`
	ActorID   string    `json:"actor_id"`
	Decision  string    `json:"decision"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type TransferApprovalRequest struct {
	ID                string                 `json:"id"`
	TenantID          string                 `json:"tenant_id"`
	TransactionID     string                 `json:"transaction_id"`
	CreatorID         string                 `json:"creator_id,omitempty"`
	Asset             string                 `json:"asset"`
	Amount            decimal.Decimal        `json:"amount"`
	RequiredApprovals int                    `json:"required_approvals"`
	ApprovalCount     int                    `json:"approval_count"`
	Status            string                 `json:"status"`
	ExpiresAt         time.Time              `json:"expires_at"`
	CreatedAt         time.Time              `json:"created_at"`
	DecidedAt         *time.Time             `json:"decided_at,omitempty"`
	Votes             []TransferApprovalVote `json:"votes"`
}
