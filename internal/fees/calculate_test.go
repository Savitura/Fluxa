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

// TestCalculate_MicroAmountFeeBypass is the regression test for the bug where
// Round(7) truncated sub-stroop fees to zero, allowing micro-transactions to
// bypass the platform fee entirely.
//
// 0.0000001 XLM × 100 bps / 10 000 = 0.000000001 XLM
// Round(7)  → 0.0000000  ← BUG: zero fee
// RoundUp(7)→ 0.0000001  ← CORRECT: one stroop
func TestCalculate_MicroAmountFeeBypass(t *testing.T) {
	amount := decimal.NewFromFloat(0.0000001) // 1 stroop
	fee, net := Calculate(amount, 100)        // 100 bps = 1 %

	if fee.IsZero() {
		t.Fatal("fee must not be zero for a non-zero amount with non-zero bps (micro-amount fee bypass)")
	}
	expectedFee := decimal.NewFromFloat(0.0000001) // ceiling of 0.000000001 at 7dp
	if !fee.Equal(expectedFee) {
		t.Fatalf("fee = %s, want %s", fee, expectedFee)
	}
	expectedNet := decimal.Zero // amount - fee = 0.0000001 - 0.0000001
	if !net.Equal(expectedNet) {
		t.Fatalf("net = %s, want %s", net, expectedNet)
	}
}

// TestCalculate_CeilingRounding ensures RoundUp(7) rounds toward positive
// infinity for any remainder, not just the exact stroop boundary.
//
// 0.0000003 XLM × 100 bps / 10 000 = 0.000000003 XLM
// RoundUp(7) → 0.0000001
func TestCalculate_CeilingRounding(t *testing.T) {
	amount := decimal.NewFromFloat(0.0000003)
	fee, _ := Calculate(amount, 100)

	if fee.IsZero() {
		t.Fatal("fee must not be zero")
	}
	expectedFee := decimal.NewFromFloat(0.0000001)
	if !fee.Equal(expectedFee) {
		t.Fatalf("fee = %s, want %s (ceiling rounding)", fee, expectedFee)
	}
}

// TestCalculate_ZeroAmountGuard verifies that a zero amount still yields a
// zero fee (the early-return guard must not be broken by the rounding change).
func TestCalculate_ZeroAmountGuard(t *testing.T) {
	fee, net := Calculate(decimal.Zero, 100)
	if !fee.IsZero() {
		t.Fatalf("fee = %s, want 0 for zero amount", fee)
	}
	if !net.IsZero() {
		t.Fatalf("net = %s, want 0 for zero amount", net)
	}
}
