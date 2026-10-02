package fees

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

type service struct {
	repo             Repository
	networkFeeSource NetworkFeeSource
}

func NewService(repo Repository) Service {
	return &service{repo: repo, networkFeeSource: StaticNetworkFeeSource{}}
}

// NewEstimatorService builds a fee service that can answer preflight network
// fee estimates using source. A nil source falls back to the protocol minimum,
// so an estimate is always available.
func NewEstimatorService(repo Repository, source NetworkFeeSource) Service {
	if source == nil {
		source = StaticNetworkFeeSource{}
	}
	return &service{repo: repo, networkFeeSource: source}
}

// Estimate returns a preflight breakdown for a transfer or batch: the platform
// fee from the caller's schedule, the Stellar network fee at the current base
// fee, and the operation/transaction counts that produced it.
func (s *service) Estimate(ctx context.Context, tenantID string, req EstimateRequest) (*NetworkFeeEstimate, error) {
	maxOps := DefaultMaxOperationsPerTransaction
	normalized, operations, transactions, err := validateEstimateRequest(req, maxOps)
	if err != nil {
		return nil, err
	}

	baseFee := DefaultBaseFeeStroops
	if s.networkFeeSource != nil {
		if fetched, fetchErr := s.networkFeeSource.BaseFeeStroops(ctx); fetchErr == nil && fetched > 0 {
			baseFee = fetched
		}
	}

	platform, err := s.calculateFee(ctx, tenantID, normalized.Asset, normalized.Amount, true)
	if err != nil {
		return nil, err
	}

	networkFeeStroops := baseFee * int64(operations)
	networkFee := decimal.NewFromInt(networkFeeStroops).Div(decimal.NewFromInt(StroopsPerUnit))

	return &NetworkFeeEstimate{
		Type:                        normalized.Type,
		Asset:                       normalized.Asset,
		GrossAmount:                 normalized.Amount.StringFixed(7),
		PlatformFee:                 platform.FeeAmount.StringFixed(7),
		PlatformFeeBps:              platform.FeeBps,
		NetAmount:                   platform.NetAmount.StringFixed(7),
		NetworkFee:                  networkFee.StringFixed(7),
		NetworkFeeStroops:           networkFeeStroops,
		TotalFee:                    platform.FeeAmount.Add(networkFee).StringFixed(7),
		BaseFeeStroops:              baseFee,
		OperationCount:              operations,
		TransactionCount:            transactions,
		MaxOperationsPerTransaction: maxOps,
		ExpiresAt:                   time.Now().UTC().Add(estimateTTL),
	}, nil
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

func (s *service) ListCollected(ctx context.Context, start, end *time.Time, tenantID *string, limit, offset int) ([]*domain.FeeCollection, error) {
	return s.repo.ListCollected(ctx, start, end, tenantID, limit, offset)
}
