package fx

import (
    "time"
    "github.com/shopspring/decimal"
)

type RateResponse struct {
    Rate          decimal.Decimal `json:"rate"`
    MidMarketRate decimal.Decimal `json:"mid_market_rate"`
    SpreadBps     int             `json:"spread_bps"`
    Provider      string          `json:"provider"`
    CachedAt      time.Time       `json:"cached_at"`
    Stale         bool            `json:"stale"`
    SourceAmount  decimal.Decimal `json:"source_amount"`
    DestAmount    decimal.Decimal `json:"dest_amount"`
    FeeAmount     decimal.Decimal `json:"fee_amount"`
    NetAmount     decimal.Decimal `json:"net_amount"`
    FeeBps        int             `json:"fee_bps"`
}
