package compliance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

// velocityMockRepo is a hand-written stub of VelocityRepository, per the
// repo's no-mocking-framework convention.
type velocityMockRepo struct {
	orgCount    int
	orgCountErr error

	destCount int
	destSum   decimal.Decimal
	destErr   error

	inbound    bool
	inboundErr error

	// Recorded arguments, so tests can assert the platform-wallet exemption
	// short-circuits before any query runs.
	calls int
}

func (m *velocityMockRepo) CountTransfersByOrgSince(_ context.Context, _ string, _ time.Time) (int, error) {
	m.calls++
	return m.orgCount, m.orgCountErr
}

func (m *velocityMockRepo) AggregateTransfersToDestinationSince(_ context.Context, _, _ string, _ time.Time) (int, decimal.Decimal, error) {
	m.calls++
	return m.destCount, m.destSum, m.destErr
}

func (m *velocityMockRepo) HasInboundTransferSince(_ context.Context, _, _ string, _ time.Time) (bool, error) {
	m.calls++
	return m.inbound, m.inboundErr
}

func baseRequest() domain.ScreeningRequest {
	return domain.ScreeningRequest{
		OrgID:        "org-1",
		FromWalletID: "wallet-src",
		ToWalletID:   "wallet-dst",
		Asset:        "USDC",
		Amount:       decimal.NewFromInt(1),
	}
}

func dec(s string) decimal.Decimal {
	d, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return d
}

func hasRule(rules []string, want string) bool {
	for _, r := range rules {
		if r == want {
			return true
		}
	}
	return false
}

// The headline acceptance criterion: three transfers of 999 are structuring,
// three of 1000 are not.
func TestStructuringRule(t *testing.T) {
	tests := []struct {
		name          string
		priorCount    int
		priorSum      string
		amount        string
		wantStructure bool
	}{
		{"3 x 999 totalling 2997 just under 3000", 2, "1998", "999", true},
		{"3 x 1000 totalling 3000 exactly on a round number", 2, "2000", "1000", false},
		{"only two transfers does not meet the minimum count", 1, "999", "999", false},
		{"exactly at the 5% tolerance", 2, "1900", "950", true},
		{"just outside the 5% tolerance", 2, "1890", "945", false},
		{"far below the next threshold", 2, "100", "50", false},
		{"crossing into the next unit stays unflagged", 2, "3000", "1000", false},
		{"large total just under 10000", 4, "7500", "2497", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &velocityMockRepo{destCount: tc.priorCount, destSum: dec(tc.priorSum)}
			v := NewVelocityScreener(repo, VelocityConfig{})

			req := baseRequest()
			req.Amount = dec(tc.amount)

			got, err := v.Screen(context.Background(), req)
			if err != nil {
				t.Fatalf("Screen returned error: %v", err)
			}

			fired := hasRule(got.RulesFired, "structuring")
			if fired != tc.wantStructure {
				t.Fatalf("structuring fired = %v, want %v (prior %d totalling %s, this one %s; rules=%v)",
					fired, tc.wantStructure, tc.priorCount, tc.priorSum, tc.amount, got.RulesFired)
			}
			if tc.wantStructure && got.Status != domain.ScreeningHold {
				t.Fatalf("status = %q, want hold", got.Status)
			}
		})
	}
}

// Structuring must only ever hold. Blocking on a behavioural signal would
// refuse legitimate payments on a probabilistic match.
func TestVelocityScreenerNeverBlocks(t *testing.T) {
	repo := &velocityMockRepo{
		orgCount:  1000,
		destCount: 99,
		destSum:   dec("2998"),
		inbound:   true,
	}
	v := NewVelocityScreener(repo, VelocityConfig{})

	req := baseRequest()
	req.Amount = dec("2")

	got, err := v.Screen(context.Background(), req)
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.Status != domain.ScreeningHold {
		t.Fatalf("status = %q, want hold even with every rule firing", got.Status)
	}
}

func TestVelocityBurstRule(t *testing.T) {
	tests := []struct {
		name      string
		priors    int
		max       int
		wantFired bool
	}{
		{"under the limit", 5, 10, false},
		{"the transfer being screened reaches the limit exactly", 9, 10, false},
		{"one over the limit", 10, 10, true},
		{"far over the limit", 50, 10, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &velocityMockRepo{orgCount: tc.priors}
			v := NewVelocityScreener(repo, VelocityConfig{MaxTransfers: tc.max})

			got, err := v.Screen(context.Background(), baseRequest())
			if err != nil {
				t.Fatalf("Screen returned error: %v", err)
			}
			if fired := hasRule(got.RulesFired, "velocity_burst"); fired != tc.wantFired {
				t.Fatalf("velocity_burst fired = %v, want %v (rules=%v)", fired, tc.wantFired, got.RulesFired)
			}
		})
	}
}

func TestRoundTripRule(t *testing.T) {
	repo := &velocityMockRepo{inbound: true}
	v := NewVelocityScreener(repo, VelocityConfig{})

	got, err := v.Screen(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if !hasRule(got.RulesFired, "round_trip") {
		t.Fatalf("round_trip did not fire; rules=%v", got.RulesFired)
	}
	if got.Status != domain.ScreeningHold {
		t.Fatalf("status = %q, want hold", got.Status)
	}
}

func TestRoundTripDoesNotFireWithoutInboundHistory(t *testing.T) {
	repo := &velocityMockRepo{inbound: false}
	v := NewVelocityScreener(repo, VelocityConfig{})

	got, err := v.Screen(context.Background(), baseRequest())
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if got.Status != domain.ScreeningClear {
		t.Fatalf("status = %q, want clear; rules=%v", got.Status, got.RulesFired)
	}
}

// Every fiat deposit, withdrawal and refund routes through the platform
// wallet, so the behavioural rules would fire constantly there and hold real
// customer refunds.
func TestPlatformWalletIsExemptFromBehaviouralRules(t *testing.T) {
	for _, leg := range []string{"from", "to"} {
		t.Run(leg, func(t *testing.T) {
			repo := &velocityMockRepo{orgCount: 1000, destCount: 99, destSum: dec("2998"), inbound: true}
			v := NewVelocityScreener(repo, VelocityConfig{PlatformWalletID: "platform-wallet"})

			req := baseRequest()
			if leg == "from" {
				req.FromWalletID = "platform-wallet"
			} else {
				req.ToWalletID = "platform-wallet"
			}

			got, err := v.Screen(context.Background(), req)
			if err != nil {
				t.Fatalf("Screen returned error: %v", err)
			}
			if got.Status != domain.ScreeningClear {
				t.Fatalf("status = %q, want clear for a platform-wallet leg; rules=%v", got.Status, got.RulesFired)
			}
			if repo.calls != 0 {
				t.Fatalf("expected the exemption to short-circuit before any query, got %d calls", repo.calls)
			}
		})
	}
}

// Each rule's repository error must surface so CompositeScreener can fail
// closed, rather than being swallowed into a clear.
func TestVelocityScreenerPropagatesRepositoryErrors(t *testing.T) {
	sentinel := errors.New("database unreachable")

	tests := []struct {
		name string
		repo *velocityMockRepo
	}{
		{"velocity query", &velocityMockRepo{orgCountErr: sentinel}},
		{"structuring query", &velocityMockRepo{destErr: sentinel}},
		{"round-trip query", &velocityMockRepo{inboundErr: sentinel}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVelocityScreener(tc.repo, VelocityConfig{})
			got, err := v.Screen(context.Background(), baseRequest())
			if !errors.Is(err, sentinel) {
				t.Fatalf("err = %v, want it to wrap %v", err, sentinel)
			}
			if got.Status == domain.ScreeningHold || got.Status == domain.ScreeningBlocked {
				t.Fatalf("errored screen should not itself assert a restrictive status, got %q", got.Status)
			}
		})
	}
}

func TestVelocityConfigDefaults(t *testing.T) {
	v := NewVelocityScreener(&velocityMockRepo{}, VelocityConfig{})

	if v.cfg.MaxTransfers != 10 {
		t.Fatalf("MaxTransfers = %d, want 10", v.cfg.MaxTransfers)
	}
	if v.cfg.Window != 10*time.Minute {
		t.Fatalf("Window = %s, want 10m", v.cfg.Window)
	}
	if v.cfg.StructuringWindow != 24*time.Hour {
		t.Fatalf("StructuringWindow = %s, want 24h", v.cfg.StructuringWindow)
	}
	if v.cfg.StructuringMinCount != 3 {
		t.Fatalf("StructuringMinCount = %d, want 3", v.cfg.StructuringMinCount)
	}
	if !v.cfg.StructuringUnit.Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("StructuringUnit = %s, want 1000", v.cfg.StructuringUnit)
	}
	if v.cfg.RoundTripWindow != 60*time.Minute {
		t.Fatalf("RoundTripWindow = %s, want 60m", v.cfg.RoundTripWindow)
	}
}

// The structuring unit is configurable; the 5%-below-a-round-number shape
// must hold at a different unit too.
func TestStructuringUnitIsConfigurable(t *testing.T) {
	repo := &velocityMockRepo{destCount: 2, destSum: dec("9900")}
	v := NewVelocityScreener(repo, VelocityConfig{StructuringUnit: decimal.NewFromInt(5000)})

	req := baseRequest()
	req.Amount = dec("50") // total 9950, next multiple of 5000 is 10000 -> 0.5% below

	got, err := v.Screen(context.Background(), req)
	if err != nil {
		t.Fatalf("Screen returned error: %v", err)
	}
	if !hasRule(got.RulesFired, "structuring") {
		t.Fatalf("structuring did not fire at unit 5000; rules=%v", got.RulesFired)
	}
}
