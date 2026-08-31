package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type FeeSchedule struct {
	ID               string
	TenantID         *string
	Asset            string
	TransferFeeBps   int
	ConversionFeeBps int
	MinFeeAmount     decimal.Decimal
	MaxFeeAmount     *decimal.Decimal
}

type FeeTier struct {
	ID               string
	TenantID         *string
	MinVolume        decimal.Decimal
	TransferFeeBps   int
	ConversionFeeBps int
}

type FeeCollection struct {
	ID           string
	TenantID     *string
	Asset        string
	FeeAmount    decimal.Decimal
	CreatedAt    time.Time
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
