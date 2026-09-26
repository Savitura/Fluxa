package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type Conversion struct {
	ID           string          `json:"id"`
	WalletID     string          `json:"wallet_id"`
	SourceAsset  string          `json:"source_asset"`
	DestAsset    string          `json:"dest_asset"`
	SourceAmount decimal.Decimal `json:"source_amount"`
	DestAmount   decimal.Decimal `json:"dest_amount"`
	FeeAmount    decimal.Decimal `json:"fee_amount"`
	FeeBps       int             `json:"fee_bps"`
	Rate         decimal.Decimal `json:"rate"`
	TxHash       string          `json:"tx_hash"`
	CreatedAt    time.Time       `json:"created_at"`
}
