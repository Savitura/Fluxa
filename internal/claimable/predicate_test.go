package claimable_test

import (
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/claimable"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/stellar/go/xdr"
)

func TestPredicateToXDRMapsAbsoluteTimes(t *testing.T) {
	before, err := claimable.PredicateToXDR(&domain.ClaimPredicate{
		Type:      domain.PredicateBeforeAbsoluteTime,
		Timestamp: 1_800_000_000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if before.Type != xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime {
		t.Fatalf("expected before_absolute_time, got %v", before.Type)
	}
	if before.AbsBefore == nil || int64(*before.AbsBefore) != 1_800_000_000 {
		t.Fatalf("expected the timestamp to survive, got %+v", before.AbsBefore)
	}

	// after_absolute_time has no native XDR arm: it must become
	// not(before_absolute_time), which is how the protocol expresses a
	// not-before constraint.
	after, err := claimable.PredicateToXDR(&domain.ClaimPredicate{
		Type:      domain.PredicateAfterAbsoluteTime,
		Timestamp: 1_800_000_000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if after.Type != xdr.ClaimPredicateTypeClaimPredicateNot {
		t.Fatalf("expected after_absolute_time to encode as not, got %v", after.Type)
	}
	if after.NotPredicate == nil || *after.NotPredicate == nil {
		t.Fatal("expected not to carry a nested predicate")
	}
	if inner := *after.NotPredicate; inner.Type != xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime {
		t.Fatalf("expected not(before_absolute_time), got %v", inner.Type)
	}
}

func TestPredicateUnconditionalAndNil(t *testing.T) {
	for name, in := range map[string]*domain.ClaimPredicate{
		"nil":           nil,
		"unconditional": {Type: domain.PredicateUnconditional},
	} {
		got, err := claimable.PredicateToXDR(in)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if got.Type != xdr.ClaimPredicateTypeClaimPredicateUnconditional {
			t.Fatalf("%s: expected unconditional, got %v", name, got.Type)
		}
	}
}

func TestPredicateRoundTrip(t *testing.T) {
	original := &domain.ClaimPredicate{
		Type: domain.PredicateOr,
		Predicates: []domain.ClaimPredicate{
			{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: 1_800_000_000},
			{
				Type: domain.PredicateAnd,
				Predicates: []domain.ClaimPredicate{
					{Type: domain.PredicateAfterAbsoluteTime, Timestamp: 1_700_000_000},
					{Type: domain.PredicateBeforeRelativeTime, Seconds: 3600},
				},
			},
		},
	}

	encoded, err := claimable.PredicateToXDR(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := claimable.PredicateFromXDR(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.Type != domain.PredicateOr {
		t.Fatalf("expected or at the root, got %q", decoded.Type)
	}
	if len(decoded.Predicates) != 2 {
		t.Fatalf("expected 2 operands, got %d", len(decoded.Predicates))
	}
	if decoded.Predicates[0].Type != domain.PredicateBeforeAbsoluteTime ||
		decoded.Predicates[0].Timestamp != 1_800_000_000 {
		t.Fatalf("first operand did not round-trip: %+v", decoded.Predicates[0])
	}
	// not(before_absolute_time) must come back out as the friendly
	// after_absolute_time rather than a raw negation.
	if got := decoded.Predicates[1].Predicates[0]; got.Type != domain.PredicateAfterAbsoluteTime ||
		got.Timestamp != 1_700_000_000 {
		t.Fatalf("after_absolute_time did not round-trip: %+v", got)
	}
	if got := decoded.Predicates[1].Predicates[1]; got.Type != domain.PredicateBeforeRelativeTime || got.Seconds != 3600 {
		t.Fatalf("before_relative_time did not round-trip: %+v", got)
	}
}

func TestPredicateRejectsMalformedInput(t *testing.T) {
	cases := map[string]*domain.ClaimPredicate{
		"unknown type":            {Type: "whenever"},
		"absolute without time":   {Type: domain.PredicateBeforeAbsoluteTime},
		"relative without offset": {Type: domain.PredicateBeforeRelativeTime},
		"not with two operands": {
			Type:       domain.PredicateNot,
			Predicates: []domain.ClaimPredicate{{Type: domain.PredicateUnconditional}, {Type: domain.PredicateUnconditional}},
		},
		"and with one operand": {
			Type:       domain.PredicateAnd,
			Predicates: []domain.ClaimPredicate{{Type: domain.PredicateUnconditional}},
		},
		"or with none": {Type: domain.PredicateOr},
	}

	for name, in := range cases {
		if _, err := claimable.PredicateToXDR(in); !errors.Is(err, domain.ErrInvalidPredicate) {
			t.Errorf("%s: expected ErrInvalidPredicate, got %v", name, err)
		}
	}
}

func TestPredicateDepthIsBounded(t *testing.T) {
	// Build a chain of `not` deeper than the cap; conversion must refuse rather
	// than recurse until the stack dies.
	deep := &domain.ClaimPredicate{Type: domain.PredicateUnconditional}
	for i := 0; i < claimable.MaxPredicateDepth+2; i++ {
		deep = &domain.ClaimPredicate{Type: domain.PredicateNot, Predicates: []domain.ClaimPredicate{*deep}}
	}
	if _, err := claimable.PredicateToXDR(deep); !errors.Is(err, domain.ErrInvalidPredicate) {
		t.Fatalf("expected ErrInvalidPredicate, got %v", err)
	}
}

func TestSatisfiableTimePredicates(t *testing.T) {
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	now := created.Add(2 * time.Hour)

	if !claimable.Satisfiable(nil, now, created) {
		t.Error("a nil predicate is unconditional and must be satisfiable")
	}

	deadline := created.Add(3 * time.Hour)
	past := created.Add(-1 * time.Hour)

	if !claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: deadline.Unix()}, now, created) {
		t.Error("before_absolute_time should hold before its deadline")
	}
	// The acceptance criterion: once before_absolute_time has passed, the
	// predicate is no longer satisfiable.
	if claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: past.Unix()}, now, created) {
		t.Error("before_absolute_time must not hold after its deadline")
	}
	// A predicate is unsatisfiable exactly at its deadline, matching Stellar's
	// "less than" wording.
	if claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: now.Unix()}, now, created) {
		t.Error("before_absolute_time must not hold at exactly its deadline")
	}

	if !claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateAfterAbsoluteTime, Timestamp: now.Unix()}, now, created) {
		t.Error("after_absolute_time should hold once its timestamp is reached")
	}
	if claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateAfterAbsoluteTime, Timestamp: deadline.Unix()}, now, created) {
		t.Error("after_absolute_time must not hold before its timestamp")
	}

	if !claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateBeforeRelativeTime, Seconds: 3 * 3600}, now, created) {
		t.Error("before_relative_time should hold within its window")
	}
	if claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateBeforeRelativeTime, Seconds: 3600}, now, created) {
		t.Error("before_relative_time must not hold once its window elapsed")
	}
}

func TestSatisfiableCompoundsAndFailsClosed(t *testing.T) {
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	now := created.Add(2 * time.Hour)

	yes := domain.ClaimPredicate{Type: domain.PredicateUnconditional}
	no := domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: created.Add(-time.Hour).Unix()}

	if !claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateAnd, Predicates: []domain.ClaimPredicate{yes, yes}}, now, created) {
		t.Error("and(yes, yes) should hold")
	}
	if claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateAnd, Predicates: []domain.ClaimPredicate{yes, no}}, now, created) {
		t.Error("and(yes, no) must not hold")
	}
	if !claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateOr, Predicates: []domain.ClaimPredicate{no, yes}}, now, created) {
		t.Error("or(no, yes) should hold")
	}
	if !claimable.Satisfiable(&domain.ClaimPredicate{Type: domain.PredicateNot, Predicates: []domain.ClaimPredicate{no}}, now, created) {
		t.Error("not(no) should hold")
	}

	// An unrecognised predicate must refuse the claim rather than allow it.
	unknown := domain.ClaimPredicate{Type: "someday"}
	if claimable.Satisfiable(&unknown, now, created) {
		t.Error("an unknown predicate must fail closed")
	}
}

func TestAnyClaimantSatisfiableAndEarliestDeadline(t *testing.T) {
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	now := created.Add(time.Hour)

	claimants := []domain.Claimant{
		{
			Account:   "GCLAIMANT1",
			Predicate: &domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: created.Add(5 * time.Hour).Unix()},
		},
		{
			Account:   "GORG",
			Predicate: &domain.ClaimPredicate{Type: domain.PredicateAfterAbsoluteTime, Timestamp: created.Add(72 * time.Hour).Unix()},
		},
	}

	if !claimable.AnyClaimantSatisfiable(claimants, now, created) {
		t.Error("expected the first claimant to be satisfiable")
	}

	deadline := claimable.EarliestDeadline(claimants)
	if deadline == nil {
		t.Fatal("expected an expiry to be derived from the absolute deadlines")
	}
	if want := created.Add(5 * time.Hour); !deadline.Equal(want) {
		t.Errorf("expected the earliest deadline %s, got %s", want, deadline)
	}
}

func TestEarliestDeadlineAbsent(t *testing.T) {
	claimants := []domain.Claimant{{Account: "GUNCONDITIONAL"}}
	if deadline := claimable.EarliestDeadline(claimants); deadline != nil {
		t.Errorf("expected no derived expiry, got %s", deadline)
	}
}
