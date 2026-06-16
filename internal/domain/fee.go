package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type FeeSchedule struct {
	ID               string
	TenantID         *string
	TransferFeeBps   int
	ConversionFeeBps int
	MinFeeAmount     decimal.Decimal
	MaxFeeAmount     *decimal.Decimal
	Asset            string
	CreatedAt        time.Time
}

type FeeCollection struct {
	ID            string
	TransactionID string
	TenantID      *string
	FeeAmount     decimal.Decimal
	Asset         string
	FeeBps        int
	CollectedAt   time.Time
}

type FeeCollectionSummary struct {
	Asset      string
	TotalFees  decimal.Decimal
	TenantFees []TenantFeeTotal
}

type TenantFeeTotal struct {
	TenantID  *string
	TotalFees decimal.Decimal
}
