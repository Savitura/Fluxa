// Package claimable implements Fluxa's claimable balance layer: creation
// (optionally reserve-sponsored), claim routing on behalf of custodied
// claimants, and background expiry tracking.
package claimable

import (
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

// MaxPredicateDepth bounds predicate nesting. Claim predicates are AND/OR trees
// of at most 2 children, so legitimate values are shallow; the cap exists so a
// hostile request body cannot turn conversion or evaluation into a stack
// overflow.
const MaxPredicateDepth = 8

// PredicateToXDR converts the declarative predicate Fluxa stores into the XDR
// form txnbuild puts on-chain.
//
// Stellar has no "after absolute time" arm — the only time arms are
// before_absolute_time and before_relative_time — so after_absolute_time is
// encoded as the negation of before_absolute_time, which is exactly how the
// protocol expresses a not-before constraint.
func PredicateToXDR(p *domain.ClaimPredicate) (xdr.ClaimPredicate, error) {
	return predicateToXDR(p, 0)
}

func predicateToXDR(p *domain.ClaimPredicate, depth int) (xdr.ClaimPredicate, error) {
	if depth > MaxPredicateDepth {
		return xdr.ClaimPredicate{}, fmt.Errorf("%w: nested more than %d levels deep", domain.ErrInvalidPredicate, MaxPredicateDepth)
	}
	if p == nil {
		return txnbuild.UnconditionalPredicate, nil
	}

	switch p.Type {
	case domain.PredicateUnconditional:
		return txnbuild.UnconditionalPredicate, nil

	case domain.PredicateBeforeAbsoluteTime:
		if p.Timestamp <= 0 {
			return xdr.ClaimPredicate{}, fmt.Errorf("%w: before_absolute_time requires a positive unix timestamp", domain.ErrInvalidPredicate)
		}
		return txnbuild.BeforeAbsoluteTimePredicate(p.Timestamp), nil

	case domain.PredicateAfterAbsoluteTime:
		if p.Timestamp <= 0 {
			return xdr.ClaimPredicate{}, fmt.Errorf("%w: after_absolute_time requires a positive unix timestamp", domain.ErrInvalidPredicate)
		}
		return txnbuild.NotPredicate(txnbuild.BeforeAbsoluteTimePredicate(p.Timestamp)), nil

	case domain.PredicateBeforeRelativeTime:
		if p.Seconds <= 0 {
			return xdr.ClaimPredicate{}, fmt.Errorf("%w: before_relative_time requires a positive number of seconds", domain.ErrInvalidPredicate)
		}
		return txnbuild.BeforeRelativeTimePredicate(p.Seconds), nil

	case domain.PredicateNot:
		child, err := singleChild(p, depth)
		if err != nil {
			return xdr.ClaimPredicate{}, err
		}
		return txnbuild.NotPredicate(child), nil

	case domain.PredicateAnd:
		left, right, err := twoChildren(p, depth)
		if err != nil {
			return xdr.ClaimPredicate{}, err
		}
		return txnbuild.AndPredicate(left, right), nil

	case domain.PredicateOr:
		left, right, err := twoChildren(p, depth)
		if err != nil {
			return xdr.ClaimPredicate{}, err
		}
		return txnbuild.OrPredicate(left, right), nil

	default:
		return xdr.ClaimPredicate{}, fmt.Errorf("%w: unknown predicate type %q", domain.ErrInvalidPredicate, p.Type)
	}
}

func singleChild(p *domain.ClaimPredicate, depth int) (xdr.ClaimPredicate, error) {
	if len(p.Predicates) != 1 {
		return xdr.ClaimPredicate{}, fmt.Errorf("%w: %s needs exactly one nested predicate", domain.ErrInvalidPredicate, p.Type)
	}
	return predicateToXDR(&p.Predicates[0], depth+1)
}

func twoChildren(p *domain.ClaimPredicate, depth int) (xdr.ClaimPredicate, xdr.ClaimPredicate, error) {
	if len(p.Predicates) != 2 {
		return xdr.ClaimPredicate{}, xdr.ClaimPredicate{}, fmt.Errorf("%w: %s needs exactly two nested predicates", domain.ErrInvalidPredicate, p.Type)
	}
	left, err := predicateToXDR(&p.Predicates[0], depth+1)
	if err != nil {
		return xdr.ClaimPredicate{}, xdr.ClaimPredicate{}, err
	}
	right, err := predicateToXDR(&p.Predicates[1], depth+1)
	if err != nil {
		return xdr.ClaimPredicate{}, xdr.ClaimPredicate{}, err
	}
	return left, right, nil
}

// PredicateFromXDR converts an XDR predicate back into the declarative form, so
// live balances read from Horizon can be compared against the stored predicate.
func PredicateFromXDR(p xdr.ClaimPredicate) (*domain.ClaimPredicate, error) {
	return predicateFromXDR(p, 0)
}

func predicateFromXDR(p xdr.ClaimPredicate, depth int) (*domain.ClaimPredicate, error) {
	if depth > MaxPredicateDepth {
		return nil, fmt.Errorf("%w: nested more than %d levels deep", domain.ErrInvalidPredicate, MaxPredicateDepth)
	}

	switch p.Type {
	case xdr.ClaimPredicateTypeClaimPredicateUnconditional:
		return &domain.ClaimPredicate{Type: domain.PredicateUnconditional}, nil

	case xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime:
		if p.AbsBefore == nil {
			return nil, fmt.Errorf("%w: before_absolute_time is missing its timestamp", domain.ErrInvalidPredicate)
		}
		return &domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: int64(*p.AbsBefore)}, nil

	case xdr.ClaimPredicateTypeClaimPredicateBeforeRelativeTime:
		if p.RelBefore == nil {
			return nil, fmt.Errorf("%w: before_relative_time is missing its offset", domain.ErrInvalidPredicate)
		}
		return &domain.ClaimPredicate{Type: domain.PredicateBeforeRelativeTime, Seconds: int64(*p.RelBefore)}, nil

	case xdr.ClaimPredicateTypeClaimPredicateNot:
		if p.NotPredicate == nil || *p.NotPredicate == nil {
			return nil, fmt.Errorf("%w: not is missing its nested predicate", domain.ErrInvalidPredicate)
		}
		child, err := predicateFromXDR(**p.NotPredicate, depth+1)
		if err != nil {
			return nil, err
		}
		// not(before_absolute_time) is how after_absolute_time is encoded;
		// surface it in the friendly form rather than the raw negation.
		if child.Type == domain.PredicateBeforeAbsoluteTime {
			return &domain.ClaimPredicate{Type: domain.PredicateAfterAbsoluteTime, Timestamp: child.Timestamp}, nil
		}
		return &domain.ClaimPredicate{Type: domain.PredicateNot, Predicates: []domain.ClaimPredicate{*child}}, nil

	case xdr.ClaimPredicateTypeClaimPredicateAnd:
		children, err := childrenFromXDR(p.AndPredicates, depth)
		if err != nil {
			return nil, err
		}
		return &domain.ClaimPredicate{Type: domain.PredicateAnd, Predicates: children}, nil

	case xdr.ClaimPredicateTypeClaimPredicateOr:
		children, err := childrenFromXDR(p.OrPredicates, depth)
		if err != nil {
			return nil, err
		}
		return &domain.ClaimPredicate{Type: domain.PredicateOr, Predicates: children}, nil

	default:
		return nil, fmt.Errorf("%w: unsupported predicate type %d", domain.ErrInvalidPredicate, p.Type)
	}
}

func childrenFromXDR(children *[]xdr.ClaimPredicate, depth int) ([]domain.ClaimPredicate, error) {
	if children == nil {
		return nil, fmt.Errorf("%w: compound predicate has no operands", domain.ErrInvalidPredicate)
	}
	out := make([]domain.ClaimPredicate, 0, len(*children))
	for i := range *children {
		child, err := predicateFromXDR((*children)[i], depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, *child)
	}
	return out, nil
}

// Satisfiable reports whether a predicate holds at now for a balance created at
// createdAt. This is a local pre-flight check, not a substitute for Stellar's
// own evaluation: it lets Fluxa reject an obviously-unsatisfiable claim before
// spending a Horizon round trip and a transaction fee.
//
// A relative predicate is measured from the balance's creation time, which is
// the only anchor Fluxa has, and matches the reference "before N seconds since
// creation" reading. A nil predicate (or an unparseable one) fails closed.
func Satisfiable(p *domain.ClaimPredicate, now, createdAt time.Time) bool {
	return satisfiable(p, now, createdAt, 0)
}

func satisfiable(p *domain.ClaimPredicate, now, createdAt time.Time, depth int) bool {
	if depth > MaxPredicateDepth {
		return false
	}
	if p == nil {
		return true
	}

	switch p.Type {
	case domain.PredicateUnconditional:
		return true
	case domain.PredicateBeforeAbsoluteTime:
		return now.Unix() < p.Timestamp
	case domain.PredicateAfterAbsoluteTime:
		return now.Unix() >= p.Timestamp
	case domain.PredicateBeforeRelativeTime:
		return now.Before(createdAt.Add(time.Duration(p.Seconds) * time.Second))
	case domain.PredicateNot:
		if len(p.Predicates) != 1 {
			return false
		}
		return !satisfiable(&p.Predicates[0], now, createdAt, depth+1)
	case domain.PredicateAnd:
		if len(p.Predicates) != 2 {
			return false
		}
		return satisfiable(&p.Predicates[0], now, createdAt, depth+1) &&
			satisfiable(&p.Predicates[1], now, createdAt, depth+1)
	case domain.PredicateOr:
		if len(p.Predicates) != 2 {
			return false
		}
		return satisfiable(&p.Predicates[0], now, createdAt, depth+1) ||
			satisfiable(&p.Predicates[1], now, createdAt, depth+1)
	default:
		// Unknown predicate kinds fail closed: refusing a claim is recoverable,
		// paying out on a predicate we do not understand is not.
		return false
	}
}

// AnyClaimantSatisfiable reports whether at least one claimant could claim now.
func AnyClaimantSatisfiable(claimants []domain.Claimant, now, createdAt time.Time) bool {
	for i := range claimants {
		if Satisfiable(claimants[i].Predicate, now, createdAt) {
			return true
		}
	}
	return false
}

// EarliestDeadline returns the earliest before_absolute_time appearing anywhere
// in the claimant set: the moment the balance stops being claimable by anybody
// and therefore its effective expiry. It returns nil when no absolute deadline
// is present, in which case the balance only expires if the caller supplies an
// explicit expiry.
func EarliestDeadline(claimants []domain.Claimant) *time.Time {
	var earliest *time.Time
	for i := range claimants {
		collectDeadlines(claimants[i].Predicate, &earliest, 0)
	}
	return earliest
}

func collectDeadlines(p *domain.ClaimPredicate, earliest **time.Time, depth int) {
	if p == nil || depth > MaxPredicateDepth {
		return
	}
	if p.Type == domain.PredicateBeforeAbsoluteTime && p.Timestamp > 0 {
		t := time.Unix(p.Timestamp, 0).UTC()
		if *earliest == nil || t.Before(**earliest) {
			*earliest = &t
		}
	}
	for i := range p.Predicates {
		collectDeadlines(&p.Predicates[i], earliest, depth+1)
	}
}
