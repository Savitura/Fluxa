package fees

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
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

	feeBps := schedule.TransferFeeBps
	if !isTransfer {
		feeBps = schedule.ConversionFeeBps
	}

	fee, net := Calculate(amount, feeBps)
	fee, net = ApplyBounds(amount, fee, schedule.MinFeeAmount, schedule.MaxFeeAmount)

	return &TransferFee{
		FeeAmount: fee,
		NetAmount: net,
		FeeBps:    feeBps,
	}, nil
}

func (s *service) RecordCollection(ctx context.Context, collection *domain.FeeCollection) error {
	return s.repo.RecordCollection(ctx, collection)
}

func (s *service) ListCollectedSummary(ctx context.Context, start, end *time.Time) ([]domain.FeeCollectionSummary, error) {
	collections, err := s.repo.ListCollected(ctx, start, end)
	if err != nil {
		return nil, err
	}

	byAsset := make(map[string]*domain.FeeCollectionSummary)
	tenantTotals := make(map[string]map[string]decimal.Decimal)

	for _, c := range collections {
		summary, ok := byAsset[c.Asset]
		if !ok {
			summary = &domain.FeeCollectionSummary{Asset: c.Asset}
			byAsset[c.Asset] = summary
			tenantTotals[c.Asset] = make(map[string]decimal.Decimal)
		}
		summary.TotalFees = summary.TotalFees.Add(c.FeeAmount)

		tenantKey := ""
		if c.TenantID != nil {
			tenantKey = *c.TenantID
		}
		tenantTotals[c.Asset][tenantKey] = tenantTotals[c.Asset][tenantKey].Add(c.FeeAmount)
	}

	result := make([]domain.FeeCollectionSummary, 0, len(byAsset))
	for asset, summary := range byAsset {
		for tenantKey, total := range tenantTotals[asset] {
			var tenantID *string
			if tenantKey != "" {
				tenantID = &tenantKey
			}
			summary.TenantFees = append(summary.TenantFees, domain.TenantFeeTotal{
				TenantID:  tenantID,
				TotalFees: total,
			})
		}
		result = append(result, *summary)
	}

	return result, nil
}
