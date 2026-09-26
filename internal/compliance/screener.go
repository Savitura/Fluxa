// Package compliance screens transfers against sanctions lists and
// suspicious-activity rules before they are enqueued for settlement, and
// owns the hold/approve/reject review workflow.
//
// The whole package fails closed: any screener that cannot reach the data it
// needs escalates to a hold rather than letting a payment through unscreened.
package compliance

import (
	"context"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/rs/zerolog"
)

// Screener evaluates one transfer. Implementations must be safe for
// concurrent use — a single instance is shared across every request.
type Screener interface {
	// Name identifies the screener in logs and in the rules_fired audit trail.
	Name() string
	// Screen returns the outcome for req. Returning an error is not a pass:
	// CompositeScreener converts it into a hold.
	Screen(ctx context.Context, req domain.ScreeningRequest) (domain.ScreeningDecision, error)
}

// CompositeScreener runs every screener and returns the most restrictive
// outcome, unioning the rules that fired.
//
// It is the single point where fail-closed is enforced: a screener that
// errors contributes a hold, and the error is logged rather than propagated,
// so one broken screener degrades the system to "review everything" instead
// of either crashing the transfer path or silently clearing payments.
type CompositeScreener struct {
	screeners []Screener
}

func NewCompositeScreener(screeners ...Screener) *CompositeScreener {
	return &CompositeScreener{screeners: screeners}
}

func (c *CompositeScreener) Name() string { return "composite" }

func (c *CompositeScreener) Screen(ctx context.Context, req domain.ScreeningRequest) (domain.ScreeningDecision, error) {
	out := domain.ScreeningDecision{Status: domain.ScreeningClear}

	for _, s := range c.screeners {
		res, err := s.Screen(ctx, req)
		if err != nil {
			zerolog.Ctx(ctx).Error().Err(err).
				Str("screener", s.Name()).
				Str("from_wallet", req.FromWalletID).
				Str("to_wallet", req.ToWalletID).
				Msg("compliance: screener failed, holding transfer")

			res = domain.ScreeningDecision{
				Status:     domain.ScreeningHold,
				RulesFired: []string{"screener_error:" + s.Name()},
				Reason:     "screening could not be completed for " + s.Name(),
				RiskScore:  50,
			}
		}

		out.RulesFired = append(out.RulesFired, res.RulesFired...)
		if res.RiskScore > out.RiskScore {
			out.RiskScore = res.RiskScore
		}
		if res.Status.Severity() > out.Status.Severity() {
			out.Status = res.Status
			out.Reason = res.Reason
			out.MatchedEntity = res.MatchedEntity
		}
	}

	return out, nil
}
