package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type CreateInput struct {
	FromWalletID string
	ToWalletID   string
	Asset        string
	Amount       decimal.Decimal
	Frequency    domain.ScheduleFrequency
	StartAt      time.Time
	EndAt        *time.Time
}

type UpdateInput struct {
	Status    *domain.ScheduleStatus
	Amount    *decimal.Decimal
	Frequency *domain.ScheduleFrequency
	EndAt     *time.Time
}

type Service interface {
	Create(ctx context.Context, in CreateInput) (*domain.Schedule, error)
	List(ctx context.Context) ([]*domain.Schedule, error)
	Update(ctx context.Context, id string, in UpdateInput) (*domain.Schedule, error)
	Cancel(ctx context.Context, id string) error
}

type service struct {
	repo       Repository
	walletRepo wallet.Repository
}

func NewService(repo Repository, walletRepo wallet.Repository) Service {
	return &service{repo: repo, walletRepo: walletRepo}
}

func (s *service) Create(ctx context.Context, in CreateInput) (*domain.Schedule, error) {
	if in.FromWalletID == in.ToWalletID {
		return nil, domain.ErrSelfTransfer
	}
	if _, err := s.walletRepo.GetByID(ctx, in.FromWalletID); err != nil {
		return nil, fmt.Errorf("source wallet: %w", err)
	}
	if _, err := s.walletRepo.GetByID(ctx, in.ToWalletID); err != nil {
		return nil, fmt.Errorf("destination wallet: %w", err)
	}

	tenantID := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}

	now := time.Now().UTC()
	sch := &domain.Schedule{
		ID:         uuid.New().String(),
		TenantID:   tenantPtr,
		FromWallet: in.FromWalletID,
		ToWallet:   in.ToWalletID,
		Asset:      in.Asset,
		Amount:     in.Amount,
		Frequency:  in.Frequency,
		NextRunAt:  in.StartAt,
		EndAt:      in.EndAt,
		Status:     domain.ScheduleStatusActive,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.repo.Create(ctx, sch); err != nil {
		return nil, fmt.Errorf("persist schedule: %w", err)
	}
	return sch, nil
}

func (s *service) List(ctx context.Context) ([]*domain.Schedule, error) {
	return s.repo.List(ctx)
}

func (s *service) Update(ctx context.Context, id string, in UpdateInput) (*domain.Schedule, error) {
	sch, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if in.Amount != nil {
		sch.Amount = *in.Amount
	}
	if in.Frequency != nil {
		sch.Frequency = *in.Frequency
	}
	if in.EndAt != nil {
		sch.EndAt = in.EndAt
	}
	if in.Status != nil {
		sch.Status = *in.Status
		if *in.Status == domain.ScheduleStatusActive {
			// Resuming a schedule whose next run already elapsed while paused
			// should not trigger an immediate burst-fire — roll forward to the
			// next future occurrence.
			now := time.Now().UTC()
			for !sch.NextRunAt.After(now) {
				sch.NextRunAt = AddInterval(sch.NextRunAt, sch.Frequency)
			}
		}
	}
	sch.UpdatedAt = time.Now().UTC()

	if err := s.repo.Update(ctx, sch); err != nil {
		return nil, fmt.Errorf("update schedule: %w", err)
	}
	return sch, nil
}

func (s *service) Cancel(ctx context.Context, id string) error {
	sch, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	sch.Status = domain.ScheduleStatusCancelled
	sch.UpdatedAt = time.Now().UTC()
	return s.repo.Update(ctx, sch)
}
