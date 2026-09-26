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
	Amount          decimal.Decimal
	Frequency       domain.ScheduleFrequency
	Timezone        string
	MissedRunPolicy domain.MissedRunPolicy
	StartAt         time.Time
	EndAt           *time.Time
}

type UpdateInput struct {
	Status          *domain.ScheduleStatus
	Amount          *decimal.Decimal
	Frequency       *domain.ScheduleFrequency
	Timezone        *string
	MissedRunPolicy *domain.MissedRunPolicy
	EndAt           *time.Time
}

type Service interface {
	Create(ctx context.Context, in CreateInput) (*domain.Schedule, error)
	List(ctx context.Context) ([]*domain.Schedule, error)
	Update(ctx context.Context, id string, in UpdateInput) (*domain.Schedule, error)
	Cancel(ctx context.Context, id string) error
	// ListRuns returns the paginated execution history for a schedule,
	// verifying tenant ownership before querying run records.
	ListRuns(ctx context.Context, scheduleID string, limit, offset int) ([]*domain.ScheduleRun, error)
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
		Asset:           in.Asset,
		Amount:          in.Amount,
		Frequency:       in.Frequency,
		Timezone:        in.Timezone,
		MissedRunPolicy: in.MissedRunPolicy,
		NextRunAt:       in.StartAt,
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
	if in.Timezone != nil {
		sch.Timezone = *in.Timezone
	}
	if in.MissedRunPolicy != nil {
		sch.MissedRunPolicy = *in.MissedRunPolicy
	}
	if in.Status != nil {
		sch.Status = *in.Status
		if *in.Status == domain.ScheduleStatusActive {
			now := time.Now().UTC()
			if sch.MissedRunPolicy == domain.MissedRunPolicySkip {
				for !sch.NextRunAt.After(now) {
					sch.NextRunAt = AddInterval(sch.NextRunAt, sch.Frequency, sch.Timezone)
				}
			} else {
				for {
					next := AddInterval(sch.NextRunAt, sch.Frequency, sch.Timezone)
					if next.After(now) {
						break
					}
					sch.NextRunAt = next
				}
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

// ListRuns returns the paginated run history for the schedule identified by
// scheduleID.  Tenant ownership is verified first: GetByID is always
// tenant-scoped, so an unauthorised caller receives ErrScheduleNotFound
// rather than a 403 that would reveal the existence of another tenant's
// schedule.
func (s *service) ListRuns(ctx context.Context, scheduleID string, limit, offset int) ([]*domain.ScheduleRun, error) {
	// Verify ownership — GetByID uses the tenant context automatically.
	if _, err := s.repo.GetByID(ctx, scheduleID); err != nil {
		return nil, err
	}
	return s.repo.ListRuns(ctx, scheduleID, limit, offset)
}
