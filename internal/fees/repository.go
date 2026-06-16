package fees

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

type Repository interface {
	GetSchedule(ctx context.Context, tenantID *string, asset string) (*domain.FeeSchedule, error)
	RecordCollection(ctx context.Context, collection *domain.FeeCollection) error
	ListCollected(ctx context.Context, start, end *time.Time) ([]*domain.FeeCollection, error)
}

type TransferFee struct {
	FeeAmount decimal.Decimal
	NetAmount decimal.Decimal
	FeeBps    int
}

type Service interface {
	GetSchedule(ctx context.Context, tenantID string) (*domain.FeeSchedule, error)
	CalculateTransferFee(ctx context.Context, tenantID, asset string, amount decimal.Decimal) (*TransferFee, error)
	CalculateConversionFee(ctx context.Context, tenantID, asset string, amount decimal.Decimal) (*TransferFee, error)
	RecordCollection(ctx context.Context, collection *domain.FeeCollection) error
	ListCollectedSummary(ctx context.Context, start, end *time.Time) ([]domain.FeeCollectionSummary, error)
}
