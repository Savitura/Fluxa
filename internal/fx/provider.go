package fx

import (
    "context"
    "github.com/shopspring/decimal"
)

// Provider defines an interface for fetching FX rates.
type Provider interface {
    // GetRate returns the mid-market rate from `from` to `to` for the given amount (as string to preserve precision).
    // The returned rate is the amount of `to` per one unit of `from`.
    GetRate(ctx context.Context, from, to, amount string) (decimal.Decimal, error)
    // SupportedPairs returns a list of currency pair strings in the form "FROM-TO" that this provider can handle.
    SupportedPairs() []string
}
