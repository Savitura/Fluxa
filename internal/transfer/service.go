package transfer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	walletpkg "github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	horizonclient "github.com/stellar/go/clients/horizonclient"
)

type TenantGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Tenant, error)
}

// Screener is the narrow view of internal/compliance this service needs.
// It is declared here, and exchanges only domain types, so the transfer
// package does not depend on the compliance package.
type Screener interface {
	ScreenTransfer(ctx context.Context, req domain.ScreeningRequest) (*domain.ScreeningDecision, error)
	RecordHold(ctx context.Context, tx *domain.Transaction, decision *domain.ScreeningDecision) error
}

type Service interface {
	InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error)
	// InitiateTransferIdempotent behaves like InitiateTransfer, but first
	// checks whether a transaction already exists for (org, idempotencyKey)
	// and returns it unchanged instead of creating a duplicate. It backs the
	// idempotency-key-protected POST /v1/transfers endpoint; callers that
	// don't need key-scoped dedup (scheduled transfers, fiat settlement) keep
	// using InitiateTransfer directly.
	InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error)
	InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error)
	GetTransaction(ctx context.Context, id string) (*domain.Transaction, error)
	ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error)
	WithStellarClient(stellarClient stellar.Client) Service
	// WithScreener enables compliance screening. It is optional so the
	// worker's screener-less wiring still compiles; when unset, transfers
	// are not screened.
	WithScreener(screener Screener) Service
}

type service struct {
	repo       Repository
	walletRepo walletpkg.Repository
	feeSvc     fees.Service
	queue      *queue.Client
	tenantRepo TenantGetter
	stellar    stellar.Client
	screener   Screener
}

func NewService(repo Repository, walletRepo walletpkg.Repository, feeSvc fees.Service, q *queue.Client, tenantRepo ...TenantGetter) Service {
	s := &service{repo: repo, walletRepo: walletRepo, feeSvc: feeSvc, queue: q}
	if len(tenantRepo) > 0 {
		s.tenantRepo = tenantRepo[0]
	}
	return s
}

func (s *service) WithStellarClient(stellarClient stellar.Client) Service {
	s.stellar = stellarClient
	return s
}

func (s *service) WithScreener(screener Screener) Service {
	s.screener = screener
	return s
}

func (s *service) InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	return s.initiate(ctx, fromID, toID, asset, amount, "", "", "")
}

func (s *service) InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error) {
	if idempotencyKey != "" {
		if existing, err := s.repo.GetByIdempotencyKey(ctx, tenant.IDFromContext(ctx), idempotencyKey); err == nil {
			return existing, nil
		} else if !errors.Is(err, domain.ErrTransactionNotFound) {
			return nil, fmt.Errorf("check idempotency key: %w", err)
		}
	}
	return s.initiate(ctx, fromID, toID, asset, amount, "", "", idempotencyKey)
}

func (s *service) InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	return s.initiate(ctx, fromID, toID, asset, amount, batchID, reference, "")
}

func (s *service) initiate(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference, idempotencyKey string) (*domain.Transaction, error) {
	if fromID == toID {
		return nil, domain.ErrSelfTransfer
	}

	tenantID := tenant.IDFromContext(ctx)
	var monthlyLimit int
	if tenantID != "" && s.tenantRepo != nil {
		t, err := s.tenantRepo.GetByID(ctx, tenantID)
		if err == nil && t != nil {
			monthlyLimit = t.GetTransferLimit()
		}
	}

	srcWallet, err := s.walletRepo.GetByID(ctx, fromID)
	if err != nil {
		return nil, fmt.Errorf("source wallet: %w", err)
	}
	dstWallet, err := s.walletRepo.GetByID(ctx, toID)
	if err != nil {
		return nil, fmt.Errorf("destination wallet: %w", err)
	}

	// Validate trustline on source wallet for non-XLM assets
	if asset != "XLM" {
		if err := s.validateTrustline(ctx, fromID, srcWallet.PublicKey, asset); err != nil {
			return nil, err
		}
	}

	// Screening runs here rather than in the handler so that batch transfers
	// and scheduled payouts, which both funnel through initiate(), are covered
	// by the same call.
	status := domain.StatusPending
	var decision *domain.ScreeningDecision
	if s.screener != nil {
		decision, err = s.screener.ScreenTransfer(ctx, domain.ScreeningRequest{
			OrgID:         tenantID,
			FromWalletID:  fromID,
			ToWalletID:    toID,
			FromPublicKey: srcWallet.PublicKey,
			ToPublicKey:   dstWallet.PublicKey,
			Asset:         asset,
			Amount:        amount,
		})
		if err != nil || decision == nil {
			// Fail closed. A screening failure must never become a pass, so an
			// unusable result is treated as a hold rather than propagated as a
			// 500 that a client would simply retry.
			decision = &domain.ScreeningDecision{
				Status:     domain.ScreeningHold,
				RulesFired: []string{"screener_error"},
				Reason:     "screening could not be completed",
				RiskScore:  50,
			}
		}

		switch decision.Status {
		case domain.ScreeningBlocked:
			// No transaction row is written: the compliance_blocks row the
			// screener already persisted is the record of this attempt.
			return nil, domain.ErrTransferBlockedSanctions
		case domain.ScreeningHold:
			status = domain.StatusComplianceHold
		}
	}

	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}

	feeResult, err := s.feeSvc.CalculateTransferFee(ctx, tenantID, asset, amount)
	if err != nil {
		return nil, fmt.Errorf("calculate transfer fee: %w", err)
	}

	var batchPtr *string
	if batchID != "" {
		batchPtr = &batchID
	}

	tx := &domain.Transaction{
		ID:             uuid.New().String(),
		Type:           domain.TypeTransfer,
		Status:         status,
		FromWallet:     fromID,
		ToWallet:       toID,
		Asset:          asset,
		Amount:         amount,
		Fee:            feeResult.FeeAmount,
		FeeBps:         feeResult.FeeBps,
		TenantID:       tenantPtr,
		BatchID:        batchPtr,
		Reference:      reference,
		CreatedAt:      time.Now().UTC(),
		IdempotencyKey: idempotencyKey,
	}

	if monthlyLimit > 0 {
		now := time.Now().UTC()
		if err := s.repo.CreateWithMonthlyLimit(ctx, tx, tenantID, now.Year(), now.Month(), monthlyLimit); err != nil {
			return nil, err
		}
	} else {
		if err := s.repo.Create(ctx, tx); err != nil {
			return nil, fmt.Errorf("persist transaction: %w", err)
		}
	}

	if tx.Status == domain.StatusComplianceHold {
		// Deliberately not enqueued. The transfer stays parked until a
		// compliance officer approves it, which resets the row to pending and
		// enqueues it then.
		if err := s.screener.RecordHold(ctx, tx, decision); err != nil {
			return nil, fmt.Errorf("record compliance hold: %w", err)
		}
		return tx, nil
	}

	if s.queue != nil {
		if err := s.queue.EnqueueTransfer(ctx, tx.ID); err != nil {
			// Transaction is persisted — worker will not run, but it can be retried.
			// Log this but don't fail the request.
			_ = err
		}
	}

	return tx, nil
}

func (s *service) validateTrustline(ctx context.Context, walletID, publicKey, asset string) error {
	hasTrustline := false

	if s.stellar != nil {
		acct, err := s.stellar.LoadAccount(publicKey)
		if err != nil {
			hErr, ok := err.(*horizonclient.Error)
			if ok && hErr.Response.Status == "404" {
				return domain.NewErrNoTrustline(asset)
			}
		} else {
			for _, b := range acct.Balances {
				if b.Code == asset {
					hasTrustline = true
					break
				}
			}
			if hasTrustline {
				return nil
			}
		}
	}

	// Fallback check in DB cached balances
	cached, err := s.walletRepo.GetBalances(ctx, walletID)
	if err == nil {
		for _, b := range cached {
			if b.AssetCode == asset {
				hasTrustline = true
				break
			}
		}
	}

	if !hasTrustline {
		return domain.NewErrNoTrustline(asset)
	}

	return nil
}

func (s *service) GetTransaction(ctx context.Context, id string) (*domain.Transaction, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *service) ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListByWallet(ctx, walletID, limit, offset)
}
