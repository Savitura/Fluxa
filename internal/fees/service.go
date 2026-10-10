package fees

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) GetSchedule(ctx context.Context, tenantID string) (*domain.FeeSchedule, error) {
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}
	return s.repo.GetSchedule(ctx, tenantPtr, "*")
}

func (s *service) SetSchedule(ctx context.Context, schedule *domain.FeeSchedule) error {
	return s.repo.SetSchedule(ctx, schedule)
}

func (s *service) CalculateTransferFee(ctx context.Context, tenantID, asset string, amount decimal.Decimal) (*TransferFee, error) {
	return s.calculateFee(ctx, tenantID, asset, amount, true)
}

func (s *service) CalculateConversionFee(ctx context.Context, tenantID, asset string, amount decimal.Decimal) (*TransferFee, error) {
	return s.calculateFee(ctx, tenantID, asset, amount, false)
}

func (s *service) calculateFee(ctx context.Context, tenantID, asset string, amount decimal.Decimal, isTransfer bool) (*TransferFee, error) {
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}

	schedule, err := s.repo.GetSchedule(ctx, tenantPtr, asset)
	if err != nil {
		return nil, err
	}

	// Look up applicable tiers based on monthly volume
	volume, err := s.repo.GetMonthlyVolume(ctx, tenantID)
	if err != nil {
		log.Error().Err(err).Str("tenant_id", tenantID).Msg("failed to get monthly volume for fee tier")
	}
	tier := s.repo.GetApplicableTier(ctx, tenantID, volume)

	feeBps := schedule.TransferFeeBps
	if !isTransfer {
		feeBps = schedule.ConversionFeeBps
	}

	if tier != nil {
		if isTransfer {
			feeBps = tier.TransferFeeBps
		} else {
			feeBps = tier.ConversionFeeBps
		}
	}

	fee, _ := Calculate(amount, feeBps)
	fee, net := ApplyBounds(amount, fee, schedule.MinFeeAmount, schedule.MaxFeeAmount)

	return &TransferFee{
		FeeAmount: fee,
		NetAmount: net,
		FeeBps:    feeBps,
	}, nil
}

func (s *service) RecordCollection(ctx context.Context, collection *domain.FeeCollection) error {
	return s.repo.RecordCollection(ctx, collection)
}

func (s *service) ListCollected(ctx context.Context, start, end *time.Time, tenantID *string, limit, offset int) ([]*domain.FeeCollection, error) {
	return s.repo.ListCollected(ctx, start, end, tenantID, limit, offset)
}
