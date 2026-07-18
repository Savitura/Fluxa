package transfer

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tenant"
	walletpkg "github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Service interface {
	InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error)
	InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error)
	GetTransaction(ctx context.Context, id string) (*domain.Transaction, error)
	ListTransactions(ctx context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error)
}

type service struct {
	repo       Repository
	walletRepo walletpkg.Repository
	feeSvc     fees.Service
	queue      *queue.Client
}

func NewService(repo Repository, walletRepo walletpkg.Repository, feeSvc fees.Service, q *queue.Client) Service {
	return &service{repo: repo, walletRepo: walletRepo, feeSvc: feeSvc, queue: q}
}

func (s *service) InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	return s.initiate(ctx, fromID, toID, asset, amount, "", "")
}

func (s *service) InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	return s.initiate(ctx, fromID, toID, asset, amount, batchID, reference)
}

func (s *service) initiate(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	if fromID == toID {
		return nil, domain.ErrSelfTransfer
	}

	if _, err := s.walletRepo.GetByID(ctx, fromID); err != nil {
		return nil, fmt.Errorf("source wallet: %w", err)
	}
	if _, err := s.walletRepo.GetByID(ctx, toID); err != nil {
		return nil, fmt.Errorf("destination wallet: %w", err)
	}

	tenantID := tenant.IDFromContext(ctx)
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
		ID:         uuid.New().String(),
		Type:       domain.TypeTransfer,
		Status:     domain.StatusPending,
		FromWallet: fromID,
		ToWallet:   toID,
		Asset:      asset,
		Amount:     amount,
		Fee:        feeResult.FeeAmount,
		FeeBps:     feeResult.FeeBps,
		TenantID:   tenantPtr,
		BatchID:    batchPtr,
		Reference:  reference,
		CreatedAt:  time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, tx); err != nil {
		return nil, fmt.Errorf("persist transaction: %w", err)
	}

	if err := s.queue.EnqueueTransfer(ctx, tx.ID); err != nil {
		// Transaction is persisted — worker will not run, but it can be retried.
		// Log this but don't fail the request.
		_ = err
	}

	return tx, nil
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
