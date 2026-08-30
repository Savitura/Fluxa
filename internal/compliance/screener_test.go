package compliance

import (
	"context"
	"errors"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
)

// stubScreener returns a canned result, per the repo's hand-written-mock
// convention.
type stubScreener struct {
	name   string
	result domain.ScreeningDecision
	err    error
}

func (s *stubScreener) Name() string { return s.name }

func (s *stubScreener) Screen(_ context.Context, _ domain.ScreeningRequest) (domain.ScreeningDecision, error) {
	return s.result, s.err
}

func clearStub(name string) *stubScreener {
	return &stubScreener{name: name, result: domain.ScreeningDecision{Status: domain.ScreeningClear}}
}

func holdStub(name string, rules ...string) *stubScreener {
	return &stubScreener{name: name, result: domain.ScreeningDecision{
		Status: domain.ScreeningHold, RulesFired: rules, Reason: "held by " + name, RiskScore: 60,
	}}
}

func blockStub(name string, rules ...string) *stubScreener {
	return &stubScreener{name: name, result: domain.ScreeningDecision{
		Status: domain.ScreeningBlocked, RulesFired: rules, Reason: "blocked by " + name, RiskScore: 100,
	}}
}

func TestCompositeScreenerPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		screeners  []Screener
		wantStatus domain.ScreeningStatus
	}{
		{"no screeners clears", nil, domain.ScreeningClear},
		{"all clear", []Screener{clearStub("a"), clearStub("b")}, domain.ScreeningClear},
		{"hold beats clear", []Screener{clearStub("a"), holdStub("b", "r")}, domain.ScreeningHold},
		{"blocked beats clear", []Screener{clearStub("a"), blockStub("b", "r")}, domain.ScreeningBlocked},
		{"blocked beats hold", []Screener{holdStub("a", "r"), blockStub("b", "r")}, domain.ScreeningBlocked},
		{"blocked first still wins", []Screener{blockStub("a", "r"), holdStub("b", "r")}, domain.ScreeningBlocked},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewCompositeScreener(tc.screeners...).Screen(context.Background(), domain.ScreeningRequest{})
			if err != nil {
				t.Fatalf("Screen returned error: %v", err)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tc.wantStatus)
			}
		})
	}
}

func TestCompositeScreenerUnionsRulesFired(t *testing.T) {
	c := NewCompositeScreener(
		holdStub("velocity", "velocity_burst", "round_trip"),
		blockStub("sanctions", "sanctions_address_match"),
	)

	got, err := c.Screen(context.Background(), domain.ScreeningRequest{})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	for _, want := range []string{"velocity_burst", "round_trip", "sanctions_address_match"} {
		if !hasRule(got.RulesFired, want) {
			t.Fatalf("rules fired = %v, missing %q", got.RulesFired, want)
		}
	}
}

func TestCompositeScreenerKeepsHighestRiskScore(t *testing.T) {
	low := &stubScreener{name: "low", result: domain.ScreeningDecision{Status: domain.ScreeningClear, RiskScore: 10}}
	high := &stubScreener{name: "high", result: domain.ScreeningDecision{Status: domain.ScreeningClear, RiskScore: 90}}

	got, err := NewCompositeScreener(low, high).Screen(context.Background(), domain.ScreeningRequest{})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.RiskScore != 90 {
		t.Fatalf("risk score = %d, want 90", got.RiskScore)
	}
}

// The central fail-closed guarantee: a broken screener degrades the system to
// "review everything", never to "clear everything".
func TestCompositeScreenerFailsClosedOnScreenerError(t *testing.T) {
	broken := &stubScreener{name: "sanctions", err: errors.New("database unreachable")}

	got, err := NewCompositeScreener(clearStub("velocity"), broken).Screen(context.Background(), domain.ScreeningRequest{})
	if err != nil {
		t.Fatalf("composite must absorb the error, got: %v", err)
	}
	if got.Status != domain.ScreeningHold {
		t.Fatalf("status = %q, want %q when a screener fails", got.Status, domain.ScreeningHold)
	}
	if !hasRule(got.RulesFired, "screener_error:sanctions") {
		t.Fatalf("rules fired = %v, want it to name the failed screener", got.RulesFired)
	}
}

// A blocked verdict from a healthy screener must survive another screener
// failing — failing closed escalates, it never de-escalates.
func TestCompositeScreenerErrorDoesNotDowngradeABlock(t *testing.T) {
	broken := &stubScreener{name: "velocity", err: errors.New("boom")}

	got, err := NewCompositeScreener(blockStub("sanctions", "sanctions_address_match"), broken).
		Screen(context.Background(), domain.ScreeningRequest{})
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.Status != domain.ScreeningBlocked {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
}

func TestScreeningStatusSeverityOrdering(t *testing.T) {
	if domain.ScreeningBlocked.Severity() <= domain.ScreeningHold.Severity() {
		t.Fatal("blocked must outrank hold")
	}
	if domain.ScreeningHold.Severity() <= domain.ScreeningClear.Severity() {
		t.Fatal("hold must outrank clear")
	}
	// An unset status must not read as a pass.
	var zero domain.ScreeningStatus
	if zero.Severity() != domain.ScreeningClear.Severity() {
		t.Fatal("zero-value severity should sort with clear, and callers must check explicitly")
	}
}
