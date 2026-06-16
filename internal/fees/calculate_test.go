package fees

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestCalculate_30bpsOn10USDC(t *testing.T) {
	amount := decimal.NewFromInt(10)
	fee, net := Calculate(amount, 30)

	expectedFee := decimal.NewFromFloat(0.03)
	expectedNet := decimal.NewFromFloat(9.97)

	if !fee.Equal(expectedFee) {
		t.Fatalf("fee = %s, want %s", fee, expectedFee)
	}
	if !net.Equal(expectedNet) {
		t.Fatalf("net = %s, want %s", net, expectedNet)
	}
}

func TestCalculate_ZeroBps(t *testing.T) {
	amount := decimal.NewFromInt(10)
	fee, net := Calculate(amount, 0)

	if !fee.IsZero() {
		t.Fatalf("fee = %s, want 0", fee)
	}
	if !net.Equal(amount) {
		t.Fatalf("net = %s, want %s", net, amount)
	}
}

func TestApplyBounds_MinFee(t *testing.T) {
	amount := decimal.NewFromFloat(1)
	fee := decimal.NewFromFloat(0.001)
	minFee := decimal.NewFromFloat(0.01)

	bounded, net := ApplyBounds(amount, fee, minFee, nil)

	if !bounded.Equal(minFee) {
		t.Fatalf("bounded fee = %s, want %s", bounded, minFee)
	}
	if !net.Equal(amount.Sub(minFee)) {
		t.Fatalf("net = %s, want %s", net, amount.Sub(minFee))
	}
}

func TestApplyBounds_MaxFee(t *testing.T) {
	amount := decimal.NewFromInt(1000)
	fee := decimal.NewFromInt(10)
	maxFee := decimal.NewFromInt(5)

	bounded, net := ApplyBounds(amount, fee, decimal.Zero, &maxFee)

	if !bounded.Equal(maxFee) {
		t.Fatalf("bounded fee = %s, want %s", bounded, maxFee)
	}
	if !net.Equal(amount.Sub(maxFee)) {
		t.Fatalf("net = %s, want %s", net, amount.Sub(maxFee))
	}
}
