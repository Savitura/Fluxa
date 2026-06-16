package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type Conversion struct {
	ID           string
	WalletID     string
	SourceAsset  string
	DestAsset    string
	SourceAmount decimal.Decimal
	DestAmount   decimal.Decimal
	FeeAmount    decimal.Decimal
	FeeBps       int
	Rate         decimal.Decimal
	TxHash       string
	CreatedAt    time.Time
}
