package compliance

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// SanctionsStatus is the payload behind GET /v1/admin/compliance/sanctions-status.
type SanctionsStatus struct {
	Loaded       bool
	EntityCount  int
	AddressCount int
	NameCount    int
	UpdatedAt    time.Time
	LastUpdate   *domain.SanctionsUpdate
}

type Service interface {
	// ScreenTransfer screens a pending transfer. A blocked outcome persists
	// the compliance_blocks row before returning, because no transaction row
	// will ever exist to hang that audit trail from.
	ScreenTransfer(ctx context.Context, req domain.ScreeningRequest) (*domain.ScreeningDecision, error)
	// RecordHold writes the review row for a transfer already persisted as
	// compliance_hold, and emits the hold webhook.
	RecordHold(ctx context.Context, tx *domain.Transaction, decision *domain.ScreeningDecision) error

	ListReviews(ctx context.Context, status string, limit, offset int) ([]*domain.ComplianceReview, error)
	GetReview(ctx context.Context, id string) (*domain.ComplianceReview, error)
	ApproveReview(ctx context.Context, id, notes string) (*domain.ComplianceReview, error)
	RejectReview(ctx context.Context, id, notes string) (*domain.ComplianceReview, error)
	SanctionsStatus(ctx context.Context) (*SanctionsStatus, error)
}

type service struct {
	repo     Repository
	screener Screener
	set      *SanctionsSet
	txGate   TransactionGate
	queue    Enqueuer
	webhooks Dispatcher
}

func NewService(repo Repository, screener Screener, set *SanctionsSet, txGate TransactionGate, q Enqueuer, webhooks Dispatcher) Service {
	return &service{
		repo:     repo,
		screener: screener,
		set:      set,
		txGate:   txGate,
		queue:    q,
		webhooks: webhooks,
	}
}

func (s *service) ScreenTransfer(ctx context.Context, req domain.ScreeningRequest) (*domain.ScreeningDecision, error) {
	decision, err := s.screener.Screen(ctx, req)
	if err != nil {
		// CompositeScreener already fails closed, but a bare Screener passed
		// in directly might not, so the same rule is enforced here.
		zerolog.Ctx(ctx).Error().Err(err).Msg("compliance: screening failed, holding transfer")
		decision = domain.ScreeningDecision{
			Status:     domain.ScreeningHold,
			RulesFired: []string{"screener_error"},
			Reason:     "screening could not be completed",
			RiskScore:  50,
		}
	}

	if decision.Status == domain.ScreeningBlocked {
		block := &domain.ComplianceBlock{
			ID:            uuid.New().String(),
			OrgID:         nullableOrg(req.OrgID),
			FromWalletID:  nullableID(req.FromWalletID),
			ToWalletID:    nullableID(req.ToWalletID),
			ToAddress:     req.ToPublicKey,
			Asset:         req.Asset,
			Amount:        amountPtr(req.Amount),
			RulesFired:    decision.RulesFired,
			Reason:        decision.Reason,
			MatchedEntity: decision.MatchedEntity,
			CreatedAt:     time.Now().UTC(),
		}
		if err := s.repo.CreateBlock(ctx, block); err != nil {
			// The block still stands — failing to write the audit row must not
			// turn a refusal into an approval.
			zerolog.Ctx(ctx).Error().Err(err).
				Str("to_wallet", req.ToWalletID).
				Msg("compliance: failed to persist block record")
		} else {
			decision.BlockID = block.ID
		}

		zerolog.Ctx(ctx).Warn().
			Str("org_id", req.OrgID).
			Str("from_wallet", req.FromWalletID).
			Str("to_wallet", req.ToWalletID).
			Str("asset", req.Asset).
			Str("matched_entity", decision.MatchedEntity).
			Strs("rules_fired", decision.RulesFired).
			Msg("compliance: transfer blocked")
	}

	return &decision, nil
}

func (s *service) RecordHold(ctx context.Context, tx *domain.Transaction, decision *domain.ScreeningDecision) error {
	review := &domain.ComplianceReview{
		ID:            uuid.New().String(),
		TransactionID: tx.ID,
		OrgID:         tx.TenantID,
		Status:        domain.ReviewPending,
		RiskScore:     decision.RiskScore,
		RulesFired:    decision.RulesFired,
		Reason:        decision.Reason,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.repo.CreateReview(ctx, review); err != nil {
		return fmt.Errorf("create compliance review: %w", err)
	}

	zerolog.Ctx(ctx).Warn().
		Str("tx_id", tx.ID).
		Str("review_id", review.ID).
		Int("risk_score", decision.RiskScore).
		Strs("rules_fired", decision.RulesFired).
		Msg("compliance: transfer held for review")

	s.dispatch(ctx, domain.EventTransferComplianceHold, map[string]interface{}{
		"transaction_id": tx.ID,
		"review_id":      review.ID,
		"risk_score":     decision.RiskScore,
		"rules_fired":    decision.RulesFired,
		"reason":         decision.Reason,
		"amount":         tx.Amount.StringFixed(7),
		"asset":          tx.Asset,
	})

	return nil
}

func (s *service) ListReviews(ctx context.Context, status string, limit, offset int) ([]*domain.ComplianceReview, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListReviews(ctx, status, limit, offset)
}

func (s *service) GetReview(ctx context.Context, id string) (*domain.ComplianceReview, error) {
	return s.repo.GetReview(ctx, id)
}

func (s *service) ApproveReview(ctx context.Context, id, notes string) (*domain.ComplianceReview, error) {
	return s.decide(ctx, id, domain.ReviewApproved, notes)
}

func (s *service) RejectReview(ctx context.Context, id, notes string) (*domain.ComplianceReview, error) {
	return s.decide(ctx, id, domain.ReviewRejected, notes)
}

func (s *service) decide(ctx context.Context, id string, outcome domain.ReviewStatus, notes string) (*domain.ComplianceReview, error) {
	review, err := s.repo.GetReview(ctx, id)
	if err != nil {
		return nil, err
	}
	if review.Status != domain.ReviewPending {
		return nil, domain.ErrReviewNotPending
	}

	// reviewedBy is empty under API-key auth, which carries no user identity.
	var reviewedBy *string
	if uid := tenant.UserIDFromContext(ctx); uid != "" {
		reviewedBy = &uid
	}

	decidedAt := time.Now().UTC()
	// DecideReview is the concurrency guard: it only transitions a row that is
	// still pending, so two simultaneous approvals cannot both go on to
	// enqueue the same transfer.
	if err := s.repo.DecideReview(ctx, id, outcome, reviewedBy, notes, decidedAt); err != nil {
		return nil, err
	}

	review.Status = outcome
	review.ReviewedBy = reviewedBy
	review.ReviewNotes = notes
	review.ReviewedAt = &decidedAt

	if outcome == domain.ReviewApproved {
		if err := s.releaseTransfer(ctx, review); err != nil {
			return nil, err
		}
		s.dispatch(ctx, domain.EventTransferComplianceApproved, reviewPayload(review))
	} else {
		if err := s.txGate.UpdateStatus(ctx, review.TransactionID, domain.StatusFailed, ""); err != nil {
			return nil, fmt.Errorf("mark rejected transfer failed: %w", err)
		}
		s.dispatch(ctx, domain.EventTransferComplianceRejected, reviewPayload(review))
	}

	zerolog.Ctx(ctx).Info().
		Str("review_id", review.ID).
		Str("tx_id", review.TransactionID).
		Str("outcome", string(outcome)).
		Msg("compliance: review decided")

	return review, nil
}

// releaseTransfer puts an approved transfer back on the settlement path.
//
// The status reset must happen before the enqueue: settlement.Engine
// silently no-ops on any status other than pending, so enqueuing a row still
// marked compliance_hold would drop the payment on the floor with no error.
func (s *service) releaseTransfer(ctx context.Context, review *domain.ComplianceReview) error {
	if err := s.txGate.UpdateStatus(ctx, review.TransactionID, domain.StatusPending, ""); err != nil {
		return fmt.Errorf("release held transfer: %w", err)
	}
	if s.queue == nil {
		return nil
	}
	if err := s.queue.EnqueueTransfer(ctx, review.TransactionID); err != nil {
		// The row is pending now, so the reconciler and a retry can still pick
		// it up; matches how transfer.initiate treats a failed enqueue.
		zerolog.Ctx(ctx).Error().Err(err).
			Str("tx_id", review.TransactionID).
			Msg("compliance: approved transfer could not be enqueued")
	}
	return nil
}

func (s *service) SanctionsStatus(ctx context.Context) (*SanctionsStatus, error) {
	status := &SanctionsStatus{}
	if s.set != nil {
		entities, addresses, names, updatedAt, loaded := s.set.Stats()
		status.EntityCount = entities
		status.AddressCount = addresses
		status.NameCount = names
		status.UpdatedAt = updatedAt
		status.Loaded = loaded
	}

	last, err := s.repo.LatestSanctionsUpdate(ctx)
	if err != nil {
		return nil, fmt.Errorf("latest sanctions update: %w", err)
	}
	status.LastUpdate = last
	return status, nil
}

func (s *service) dispatch(ctx context.Context, event domain.EventType, payload interface{}) {
	if s.webhooks == nil {
		return
	}
	if err := s.webhooks.Dispatch(ctx, event, payload); err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Str("event", string(event)).Msg("compliance: webhook dispatch failed")
	}
}

func reviewPayload(r *domain.ComplianceReview) map[string]interface{} {
	return map[string]interface{}{
		"review_id":      r.ID,
		"transaction_id": r.TransactionID,
		"status":         string(r.Status),
		"rules_fired":    r.RulesFired,
		"review_notes":   r.ReviewNotes,
	}
}

func nullableOrg(orgID string) *string {
	if orgID == "" {
		return nil
	}
	return &orgID
}

func nullableID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

func amountPtr(a decimal.Decimal) *decimal.Decimal {
	if a.IsZero() {
		return nil
	}
	return &a
}
