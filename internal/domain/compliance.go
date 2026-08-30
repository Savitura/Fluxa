package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// ScreeningStatus is the outcome of screening a single transfer. The zero
// value is deliberately not "clear": an unset status must never read as a
// pass, because the screening path fails closed.
type ScreeningStatus string

const (
	ScreeningClear   ScreeningStatus = "clear"
	ScreeningHold    ScreeningStatus = "hold"
	ScreeningBlocked ScreeningStatus = "blocked"
)

// Severity orders screening outcomes so a composite screener can pick the
// most restrictive result: blocked > hold > clear.
func (s ScreeningStatus) Severity() int {
	switch s {
	case ScreeningBlocked:
		return 2
	case ScreeningHold:
		return 1
	default:
		return 0
	}
}

// ScreeningRequest is the input to compliance screening. It lives in domain
// so internal/transfer can declare a narrow screener interface without
// importing internal/compliance.
type ScreeningRequest struct {
	OrgID         string
	FromWalletID  string
	ToWalletID    string
	FromPublicKey string
	ToPublicKey   string
	// ToFederation is the destination's federation / display name, used for
	// fuzzy name matching against the SDN list. Empty when unknown.
	ToFederation string
	Asset        string
	Amount       decimal.Decimal
}

// ScreeningDecision is the result of screening, including the audit-trail row
// ids written as a side effect.
type ScreeningDecision struct {
	Status ScreeningStatus
	// RulesFired names every rule that matched, across all screeners.
	RulesFired []string
	Reason     string
	RiskScore  int
	// MatchedEntity is the sanctions entity name on a sanctions match.
	MatchedEntity string
	// BlockID is set only when Status is blocked, referencing the
	// compliance_blocks row already persisted.
	BlockID string
}

type ReviewStatus string

const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewRejected ReviewStatus = "rejected"
)

// ComplianceReview is the hold/approve/reject record for one held transfer.
type ComplianceReview struct {
	ID            string
	TransactionID string
	OrgID         *string
	Status        ReviewStatus
	RiskScore     int
	RulesFired    []string
	Reason        string
	// ReviewedBy is the user id that decided the review. It is empty for
	// API-key authenticated approvals, which carry no user identity.
	ReviewedBy  *string
	ReviewNotes string
	ReviewedAt  *time.Time
	CreatedAt   time.Time
}

// ComplianceBlock records a transfer that was refused outright. No
// transaction row exists for it, so this is the only trace of the attempt.
type ComplianceBlock struct {
	ID            string
	OrgID         *string
	FromWalletID  *string
	ToWalletID    *string
	ToAddress     string
	Asset         string
	Amount        *decimal.Decimal
	RulesFired    []string
	Reason        string
	MatchedEntity string
	CreatedAt     time.Time
}

// SanctionsEntity is one parsed SDN record. A single SDN entry expands to one
// entity per digital-currency address plus one per name/alias, so Address is
// empty on the name-only rows.
type SanctionsEntity struct {
	UID         string
	Name        string
	EntityType  string
	Address     string
	AddressType string
	Programs    []string
	Source      string
	RefreshedAt time.Time
}

type SanctionsUpdateStatus string

const (
	SanctionsUpdateSuccess SanctionsUpdateStatus = "success"
	SanctionsUpdateFailed  SanctionsUpdateStatus = "failed"
)

// SanctionsUpdate is the audit row for one SDN refresh attempt.
type SanctionsUpdate struct {
	ID          string
	Source      string
	Status      SanctionsUpdateStatus
	EntityCount int
	DurationMS  int64
	Error       string
	StartedAt   time.Time
	FinishedAt  time.Time
}
