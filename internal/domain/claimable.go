package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// ClaimableBalanceStatus is the lifecycle state of a claimable balance Fluxa
// created on behalf of an org.
type ClaimableBalanceStatus string

const (
	// ClaimableBalanceStatusPending is a balance that is live on Stellar and
	// has not yet been claimed or revoked.
	ClaimableBalanceStatusPending ClaimableBalanceStatus = "pending"
	// ClaimableBalanceStatusClaimed means a claimant claimed it.
	ClaimableBalanceStatusClaimed ClaimableBalanceStatus = "claimed"
	// ClaimableBalanceStatusRevoked means Fluxa claimed it back to the org
	// because revoke_on_expiry was set and it expired unclaimed.
	ClaimableBalanceStatusRevoked ClaimableBalanceStatus = "revoked"
	// ClaimableBalanceStatusExpired means the balance passed its expiry and
	// was marked expired without being clawed back on-chain.
	ClaimableBalanceStatusExpired ClaimableBalanceStatus = "expired"
)

// PredicateType enumerates the claim predicate kinds Fluxa supports. They map
// one-to-one onto xdr.ClaimPredicateType; the declarative shape below is what
// the API accepts so callers never have to speak XDR.
type PredicateType string

const (
	// PredicateUnconditional is always satisfiable.
	PredicateUnconditional PredicateType = "unconditional"
	// PredicateBeforeAbsoluteTime is satisfiable while now < Timestamp.
	PredicateBeforeAbsoluteTime PredicateType = "before_absolute_time"
	// PredicateAfterAbsoluteTime is satisfiable while now >= Timestamp. Stellar
	// has no native arm for it, so it is encoded as not(before_absolute_time).
	PredicateAfterAbsoluteTime PredicateType = "after_absolute_time"
	// PredicateBeforeRelativeTime is satisfiable while now < CreatedAt+Seconds.
	PredicateBeforeRelativeTime PredicateType = "before_relative_time"
	// PredicateNot inverts its single child predicate.
	PredicateNot PredicateType = "not"
	// PredicateAnd requires both of its child predicates.
	PredicateAnd PredicateType = "and"
	// PredicateOr requires at least one of its child predicates.
	PredicateOr PredicateType = "or"
)

// ClaimPredicate is the declarative form of an xdr.ClaimPredicate. Exactly the
// fields relevant to Type are populated; internal/claimable converts it to and
// from XDR.
type ClaimPredicate struct {
	Type PredicateType `json:"type"`
	// Timestamp is the Unix timestamp (seconds) used by the absolute-time
	// predicates.
	Timestamp int64 `json:"timestamp,omitempty"`
	// Seconds is the relative offset used by before_relative_time, measured
	// from the moment the balance was created.
	Seconds int64 `json:"seconds,omitempty"`
	// Predicates holds the operands: one for not, two for and/or.
	Predicates []ClaimPredicate `json:"predicates,omitempty"`
}

// Claimant is one account allowed to claim a balance together with the
// predicate that has to hold at claim time. A nil Predicate means
// unconditional.
type Claimant struct {
	Account   string          `json:"account"`
	Predicate *ClaimPredicate `json:"predicate,omitempty"`
}

// ClaimableBalance is a deferred payment held by the Stellar network until one
// of its claimants satisfies their predicate.
type ClaimableBalance struct {
	// ID is the Stellar claimable balance ID.
	ID             string
	TenantID       *string
	Asset          string
	Amount         decimal.Decimal
	Claimants      []Claimant
	Sponsor        string
	Status         ClaimableBalanceStatus
	RevokeOnExpiry bool
	CreatedAt      time.Time
	ExpiresAt      *time.Time
	ClaimedAt      *time.Time
	ClaimedBy      string
}
