package fees

import (
	"github.com/shopspring/decimal"
)

var bpsDivisor = decimal.NewFromInt(10000)

// Calculate computes the platform fee and net amount from a gross amount and basis points.
func Calculate(amount decimal.Decimal, feeBps int) (fee, net decimal.Decimal) {
	if feeBps <= 0 || amount.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, amount
	}

	fee = amount.Mul(decimal.NewFromInt(int64(feeBps))).Div(bpsDivisor).Round(7)
	net = amount.Sub(fee)
	return fee, net
}

// ApplyBounds clamps fee to min/max schedule limits and recomputes net.
func ApplyBounds(amount, fee decimal.Decimal, minFee decimal.Decimal, maxFee *decimal.Decimal) (boundedFee, net decimal.Decimal) {
	boundedFee = fee
	if boundedFee.LessThan(minFee) {
		boundedFee = minFee
	}
	if maxFee != nil && boundedFee.GreaterThan(*maxFee) {
		boundedFee = *maxFee
	}
	if boundedFee.GreaterThan(amount) {
		boundedFee = amount
	}
	net = amount.Sub(boundedFee)
	return boundedFee, net
}
