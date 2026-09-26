package fx

import (
	"strings"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

// NativeAssetCode is the Stellar native asset (lumens).
const NativeAssetCode = "XLM"

// XLM amount limits for FX conversions (stroop precision floor, operational cap).
var (
	xlmMinAmount = decimal.RequireFromString("0.0000001")
	xlmMaxAmount = decimal.NewFromInt(1_000_000)
)

// supportedFXAssets are the asset codes the FX service will quote.
var supportedFXAssets = map[string]struct{}{
	"XLM":  {},
	"USDC": {},
	"EURC": {},
}

// IsNativeAsset reports whether code is the Stellar native asset (XLM).
func IsNativeAsset(code string) bool {
	return strings.EqualFold(strings.TrimSpace(code), NativeAssetCode)
}

// AssetRequiresTrustline is false for native XLM (no trustline needed) and
// true for issued assets that must be trusted before hold/receive.
func AssetRequiresTrustline(code string) bool {
	return !IsNativeAsset(code)
}

func normalizeAsset(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

func isSupportedFXAsset(code string) bool {
	_, ok := supportedFXAssets[normalizeAsset(code)]
	return ok
}

// validateFXPair normalizes and validates a conversion pair.
func validateFXPair(from, to string) (string, string, error) {
	from = normalizeAsset(from)
	to = normalizeAsset(to)
	if from == "" || to == "" || !isSupportedFXAsset(from) || !isSupportedFXAsset(to) {
		return "", "", domain.ErrInvalidAsset
	}
	if from == to {
		return "", "", domain.ErrInvalidAsset
	}
	return from, to, nil
}

// validateAmountLimits enforces per-asset min/max for quote amounts.
// XLM uses stroop-precision minimum and a hard operational maximum.
func validateAmountLimits(asset string, amount decimal.Decimal) error {
	if !IsNativeAsset(asset) {
		return nil
	}
	if amount.LessThan(xlmMinAmount) || amount.GreaterThan(xlmMaxAmount) {
		return domain.ErrAmountOutOfLimits
	}
	return nil
}

// DefaultXLMFXPairs are the native↔token pairs the Horizon + oracle providers serve.
func DefaultXLMFXPairs() []string {
	return []string{
		"XLM-USDC", "USDC-XLM",
		"XLM-EURC", "EURC-XLM",
	}
}
