package transferapproval

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

var ErrInvalidPolicy = errors.New("invalid transfer approval policy")

type Repository interface {
	GetPolicy(context.Context, string, string) (*domain.TransferApprovalPolicy, error)
	PutPolicy(context.Context, *domain.TransferApprovalPolicy) error
	CreateRequest(context.Context, *domain.TransferApprovalRequest) error
	ListRequests(context.Context, string, string, int, int) ([]*domain.TransferApprovalRequest, int, error)
	CastVote(context.Context, string, string, string, string, string) (*domain.TransferApprovalRequest, bool, error)
	ExpirePending(context.Context, int) ([]domain.ExpiredTransferApproval, error)
}

// WebhookDispatcher delivers tenant webhooks; the tenant comes from ctx.
type WebhookDispatcher interface {
	Dispatch(ctx context.Context, eventType domain.EventType, payload interface{}) error
}

type Enqueuer interface {
	EnqueueTransfer(context.Context, string) error
}

type Service struct {
	repo     Repository
	queue    Enqueuer
	webhooks WebhookDispatcher
	now      func() time.Time
}

// expiryBatchSize bounds each ExpirePending statement so one sweep never holds
// locks on an unbounded number of rows.
const expiryBatchSize = 500

// ExpiryReport summarises one expiry sweep.
type ExpiryReport struct {
	Expired            int
	TransactionsFailed int
}

func NewService(repo Repository, queue Enqueuer) *Service {
	return &Service{repo: repo, queue: queue, now: func() time.Time { return time.Now().UTC() }}
}

// WithWebhooks enables transfer.failed webhooks for transfers whose approval
// expires in the background sweep.
func (s *Service) WithWebhooks(webhooks WebhookDispatcher) *Service {
	s.webhooks = webhooks
	return s
}

// ExpireDue expires every pending approval request past its expires_at and
// fails the held transaction, so funds are not left looking committed when
// nobody opens the approvals list. Tenants are told via transfer.failed.
func (s *Service) ExpireDue(ctx context.Context) (ExpiryReport, error) {
	var report ExpiryReport
	for {
		expired, err := s.repo.ExpirePending(ctx, expiryBatchSize)
		if err != nil {
			return report, err
		}
		for _, e := range expired {
			report.Expired++
			if !e.TransactionFailed {
				continue
			}
			report.TransactionsFailed++
			s.dispatchExpired(ctx, e)
		}
		if len(expired) < expiryBatchSize {
			return report, nil
		}
	}
}

func (s *Service) dispatchExpired(ctx context.Context, e domain.ExpiredTransferApproval) {
	if s.webhooks == nil {
		return
	}
	payload := map[string]interface{}{
		"transaction_id":      e.TransactionID,
		"event":               domain.EventTransferFailed,
		"amount":              e.Amount.String(),
		"asset":               e.Asset,
		"failure_reason":      "approval expired",
		"approval_request_id": e.RequestID,
	}
	if err := s.webhooks.Dispatch(tenant.WithID(ctx, e.TenantID), domain.EventTransferFailed, payload); err != nil {
		log.Error().Err(err).Str("transaction_id", e.TransactionID).Str("tenant_id", e.TenantID).
			Msg("transferapproval: expired approval webhook dispatch failed")
	}
}

// Plan returns an enabled approval policy only when a transfer meets its
// per-asset threshold. No policy means the existing transfer flow is unchanged.
func (s *Service) Plan(ctx context.Context, tenantID, asset string, amount decimal.Decimal) (*domain.TransferApprovalPolicy, error) {
	if tenantID == "" {
		return nil, nil
	}
	policy, err := s.repo.GetPolicy(ctx, tenantID, strings.ToUpper(strings.TrimSpace(asset)))
	if err != nil {
		return nil, err
	}
	if policy == nil || !policy.Enabled || amount.LessThan(policy.Threshold) {
		return nil, nil
	}
	return policy, nil
}

func (s *Service) CreateRequest(ctx context.Context, tx *domain.Transaction, creator domain.TransferApprovalCreator, policy *domain.TransferApprovalPolicy) error {
	if policy == nil || tx == nil || tx.TenantID == nil {
		return errors.New("approval request requires a tenant-scoped transaction and policy")
	}
	expiresAfter := policy.ExpiresAfter
	if expiresAfter <= 0 {
		expiresAfter = 24 * time.Hour
	}
	request := &domain.TransferApprovalRequest{
		ID: uuid.NewString(), TenantID: *tx.TenantID, TransactionID: tx.ID,
		CreatorID: creator.UserID, CreatorAPIKeyID: creator.APIKeyID,
		Asset: tx.Asset, Amount: tx.Amount, RequiredApprovals: policy.RequiredApprovals,
		Status: "pending", ExpiresAt: s.now().Add(expiresAfter), CreatedAt: s.now(),
	}
	if err := s.repo.CreateRequest(ctx, request); err != nil {
		return fmt.Errorf("create transfer approval request: %w", err)
	}
	return nil
}

func (s *Service) GetPolicy(ctx context.Context, asset string) (*domain.TransferApprovalPolicy, error) {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return nil, domain.ErrForbidden
	}
	return s.repo.GetPolicy(ctx, tenantID, strings.ToUpper(strings.TrimSpace(asset)))
}

func (s *Service) PutPolicy(ctx context.Context, policy *domain.TransferApprovalPolicy) error {
	if policy == nil || strings.TrimSpace(policy.Asset) == "" || !policy.Threshold.GreaterThan(decimal.Zero) ||
		policy.RequiredApprovals < 1 || policy.RequiredApprovals > 10 ||
		policy.ExpiresAfter < time.Minute || policy.ExpiresAfter > 30*24*time.Hour {
		return ErrInvalidPolicy
	}
	policy.TenantID = tenant.IDFromContext(ctx)
	policy.Asset = strings.ToUpper(strings.TrimSpace(policy.Asset))
	if policy.TenantID == "" {
		return domain.ErrForbidden
	}
	return s.repo.PutPolicy(ctx, policy)
}

func (s *Service) List(ctx context.Context, status string, limit, offset int) ([]*domain.TransferApprovalRequest, int, error) {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return nil, 0, domain.ErrForbidden
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListRequests(ctx, tenantID, status, limit, offset)
}

func (s *Service) Decide(ctx context.Context, requestID, decision, note string) (*domain.TransferApprovalRequest, error) {
	tenantID := tenant.IDFromContext(ctx)
	actorID := tenant.UserIDFromContext(ctx)
	if tenantID == "" || actorID == "" {
		return nil, domain.ErrForbidden
	}
	if decision != "approved" && decision != "rejected" {
		return nil, ErrInvalidPolicy
	}
	request, release, err := s.repo.CastVote(ctx, tenantID, requestID, actorID, decision, note)
	if err != nil {
		return nil, err
	}
	if release && s.queue != nil {
		if err := s.queue.EnqueueTransfer(ctx, request.TransactionID); err != nil {
			return request, fmt.Errorf("approval recorded; settlement enqueue will be recovered: %w", err)
		}
	}
	return request, nil
}
