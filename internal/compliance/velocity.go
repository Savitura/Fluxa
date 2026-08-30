package compliance

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

// VelocityConfig tunes the three suspicious-activity rules. Zero fields fall
// back to the defaults applied in NewVelocityScreener.
type VelocityConfig struct {
	// Velocity: more than MaxTransfers from one org inside Window holds.
	Window       time.Duration
	MaxTransfers int

	// Structuring: at least MinCount transfers to one destination inside
	// StructuringWindow whose running total sits just under a round number.
	StructuringWindow    time.Duration
	StructuringMinCount  int
	StructuringUnit      decimal.Decimal
	StructuringTolerance decimal.Decimal

	// Round-trip: funds coming back from the destination inside RoundTripWindow.
	RoundTripWindow time.Duration

	// PlatformWalletID is exempt from the behavioural rules. Every fiat
	// deposit, withdrawal and refund legitimately routes through this wallet,
	// so velocity and round-trip would fire constantly on it and hold real
	// customer refunds. Sanctions screening is never skipped.
	PlatformWalletID string
}

// VelocityRepository is the narrow slice of transaction history the
// behavioural rules query.
type VelocityRepository interface {
	// CountTransfersByOrgSince counts transfers created by orgID since ts.
	CountTransfersByOrgSince(ctx context.Context, orgID string, since time.Time) (int, error)
	// AggregateTransfersToDestinationSince returns how many transfers orgID
	// has sent to toWalletID since ts, and their total amount.
	AggregateTransfersToDestinationSince(ctx context.Context, orgID, toWalletID string, since time.Time) (int, decimal.Decimal, error)
	// HasInboundTransferSince reports whether walletID has received a transfer
	// from counterpartyWalletID since ts.
	HasInboundTransferSince(ctx context.Context, walletID, counterpartyWalletID string, since time.Time) (bool, error)
}

// VelocityScreener implements the three behavioural rules. Unlike the
// sanctions screener it can only ever hold, never block: these are
// probabilistic signals about behaviour, not identity matches.
type VelocityScreener struct {
	repo VelocityRepository
	cfg  VelocityConfig
}

func NewVelocityScreener(repo VelocityRepository, cfg VelocityConfig) *VelocityScreener {
	if cfg.Window <= 0 {
		cfg.Window = 10 * time.Minute
	}
	if cfg.MaxTransfers <= 0 {
		cfg.MaxTransfers = 10
	}
	if cfg.StructuringWindow <= 0 {
		cfg.StructuringWindow = 24 * time.Hour
	}
	if cfg.StructuringMinCount <= 0 {
		cfg.StructuringMinCount = 3
	}
	if cfg.StructuringUnit.LessThanOrEqual(decimal.Zero) {
		cfg.StructuringUnit = decimal.NewFromInt(1000)
	}
	if cfg.StructuringTolerance.LessThanOrEqual(decimal.Zero) {
		cfg.StructuringTolerance = decimal.NewFromFloat(0.05)
	}
	if cfg.RoundTripWindow <= 0 {
		cfg.RoundTripWindow = 60 * time.Minute
	}
	return &VelocityScreener{repo: repo, cfg: cfg}
}

func (v *VelocityScreener) Name() string { return "velocity" }

func (v *VelocityScreener) Screen(ctx context.Context, req domain.ScreeningRequest) (domain.ScreeningDecision, error) {
	clear := domain.ScreeningDecision{Status: domain.ScreeningClear}

	if v.cfg.PlatformWalletID != "" &&
		(req.FromWalletID == v.cfg.PlatformWalletID || req.ToWalletID == v.cfg.PlatformWalletID) {
		return clear, nil
	}

	var rules []string
	var reasons []string
	score := 0

	now := time.Now().UTC()

	if req.OrgID != "" {
		count, err := v.repo.CountTransfersByOrgSince(ctx, req.OrgID, now.Add(-v.cfg.Window))
		if err != nil {
			return clear, fmt.Errorf("velocity count: %w", err)
		}
		// count excludes the transfer being screened, which has not been
		// persisted yet, so +1 counts it.
		if count+1 > v.cfg.MaxTransfers {
			rules = append(rules, "velocity_burst")
			reasons = append(reasons, fmt.Sprintf("%d transfers in %s exceeds the limit of %d",
				count+1, v.cfg.Window, v.cfg.MaxTransfers))
			score = maxInt(score, 60)
		}
	}

	if req.ToWalletID != "" {
		count, sum, err := v.repo.AggregateTransfersToDestinationSince(
			ctx, req.OrgID, req.ToWalletID, now.Add(-v.cfg.StructuringWindow))
		if err != nil {
			return clear, fmt.Errorf("structuring aggregate: %w", err)
		}
		total := sum.Add(req.Amount)
		if count+1 >= v.cfg.StructuringMinCount && v.isStructuring(total) {
			rules = append(rules, "structuring")
			reasons = append(reasons, fmt.Sprintf(
				"%d transfers to the same destination totalling %s sit just below a round threshold",
				count+1, total.StringFixed(2)))
			score = maxInt(score, 70)
		}
	}

	if req.FromWalletID != "" && req.ToWalletID != "" {
		inbound, err := v.repo.HasInboundTransferSince(
			ctx, req.FromWalletID, req.ToWalletID, now.Add(-v.cfg.RoundTripWindow))
		if err != nil {
			return clear, fmt.Errorf("round-trip lookup: %w", err)
		}
		if inbound {
			rules = append(rules, "round_trip")
			reasons = append(reasons, fmt.Sprintf(
				"source received funds from this destination within the last %s", v.cfg.RoundTripWindow))
			score = maxInt(score, 55)
		}
	}

	if len(rules) == 0 {
		return clear, nil
	}

	return domain.ScreeningDecision{
		Status:     domain.ScreeningHold,
		RulesFired: rules,
		Reason:     joinReasons(reasons),
		RiskScore:  score,
	}, nil
}

// isStructuring reports whether total sits within StructuringTolerance below
// the next multiple of StructuringUnit.
//
// R is the next multiple *strictly greater* than total, which is what makes an
// exactly-round sum fall through cleanly:
//
//	3 x 999  = 2997 -> R = 3000 -> (3000-2997)/3000 = 0.001 <= 0.05  -> flagged
//	3 x 1000 = 3000 -> R = 4000 -> (4000-3000)/4000 = 0.25  >  0.05  -> not flagged
//
// A ladder of coarser thresholds (1000/5000/10000) would miss the 2997 case,
// so the unit stays a single configurable value.
func (v *VelocityScreener) isStructuring(total decimal.Decimal) bool {
	if total.LessThanOrEqual(decimal.Zero) {
		return false
	}
	unit := v.cfg.StructuringUnit
	next := total.Div(unit).Floor().Add(decimal.NewFromInt(1)).Mul(unit)
	if next.LessThanOrEqual(decimal.Zero) {
		return false
	}
	return next.Sub(total).Div(next).LessThanOrEqual(v.cfg.StructuringTolerance)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func joinReasons(reasons []string) string {
	out := ""
	for i, r := range reasons {
		if i > 0 {
			out += "; "
		}
		out += r
	}
	return out
}
