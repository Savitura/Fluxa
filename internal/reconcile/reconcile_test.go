package reconcile

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stellar/go/protocols/horizon/operations"
)

// mustPaymentOp builds an operations.Payment fixture from raw Horizon JSON
// field names (rather than Go struct literals) so the fixture depends only
// on the stable, documented Horizon API contract.
func mustPaymentOp(t *testing.T, from, to, amount, assetType, assetCode, assetIssuer string) operations.Payment {
	t.Helper()
	raw := `{
		"type": "payment",
		"from": "` + from + `",
		"to": "` + to + `",
		"amount": "` + amount + `",
		"asset_type": "` + assetType + `",
		"asset_code": "` + assetCode + `",
		"asset_issuer": "` + assetIssuer + `"
	}`
	var p operations.Payment
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal fixture payment op: %v", err)
	}
	return p
}

func mustNonPaymentOp(t *testing.T, opType string) operations.Operation {
	t.Helper()
	raw := `{"type": "` + opType + `"}`
	var p operations.CreateAccount
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal fixture non-payment op: %v", err)
	}
	return p
}

const (
	platformFrom = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAPLATFROM"
	platformTo   = "GBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBPLATFTO"
	feeWallet    = "GCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCFEEWALT"
	attackerAddr = "GDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDATTACKER"
	realIssuer   = "GEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEISSUER1"
	fakeIssuer   = "GFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFISSUER2"
)

func usdcExpected(net, fee string) expectedPayment {
	return expectedPayment{
		FromPublicKey:      platformFrom,
		ToPublicKey:        platformTo,
		AssetCode:          "USDC",
		AssetIssuer:        realIssuer,
		NetAmount:          decimal.RequireFromString(net),
		Fee:                decimal.RequireFromString(fee),
		FeeWalletPublicKey: feeWallet,
	}
}

func TestVerifyOps_LegitimatePaymentNoFee(t *testing.T) {
	expected := usdcExpected("100.0000000", "0")
	ops := []operations.Operation{
		mustPaymentOp(t, platformFrom, platformTo, "100.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	amountOK, assetOK, feeOK, details := verifyOps(ops, expected)
	if !amountOK || !assetOK || !feeOK {
		t.Fatalf("expected full match, got amount=%v asset=%v fee=%v details=%q", amountOK, assetOK, feeOK, details)
	}
}

func TestVerifyOps_LegitimatePaymentWithFeeLeg(t *testing.T) {
	expected := usdcExpected("95.0000000", "5.0000000")
	ops := []operations.Operation{
		mustPaymentOp(t, platformFrom, platformTo, "95.0000000", "credit_alphanum4", "USDC", realIssuer),
		mustPaymentOp(t, platformFrom, feeWallet, "5.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	amountOK, assetOK, feeOK, details := verifyOps(ops, expected)
	if !amountOK || !assetOK || !feeOK {
		t.Fatalf("expected full match, got amount=%v asset=%v fee=%v details=%q", amountOK, assetOK, feeOK, details)
	}
}

func TestVerifyOps_MissingFeeLegIsRejected(t *testing.T) {
	expected := usdcExpected("95.0000000", "5.0000000")
	ops := []operations.Operation{
		// Main leg present and correct, but the platform fee was never collected.
		mustPaymentOp(t, platformFrom, platformTo, "95.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	amountOK, assetOK, feeOK, _ := verifyOps(ops, expected)
	if !amountOK || !assetOK {
		t.Fatalf("expected main leg to verify, got amount=%v asset=%v", amountOK, assetOK)
	}
	if feeOK {
		t.Fatal("expected feeVerified=false when the fee leg is absent")
	}
}

// TestVerifyOps_WrongDestinationIsRejected is the core #97 regression case:
// an operation moving the exact expected amount and asset, but to an
// unrelated (attacker-controlled) account, must not be accepted just because
// the amount and asset code matched.
func TestVerifyOps_WrongDestinationIsRejected(t *testing.T) {
	expected := usdcExpected("100.0000000", "0")
	ops := []operations.Operation{
		mustPaymentOp(t, platformFrom, attackerAddr, "100.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	amountOK, assetOK, _, _ := verifyOps(ops, expected)
	if amountOK || assetOK {
		t.Fatalf("expected rejection of a matching-amount/asset payment to the wrong destination, got amount=%v asset=%v", amountOK, assetOK)
	}
}

// TestVerifyOps_WrongSourceIsRejected: same amount/asset/destination, but
// sent from an account other than the transaction's expected source wallet.
func TestVerifyOps_WrongSourceIsRejected(t *testing.T) {
	expected := usdcExpected("100.0000000", "0")
	ops := []operations.Operation{
		mustPaymentOp(t, attackerAddr, platformTo, "100.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	amountOK, assetOK, _, _ := verifyOps(ops, expected)
	if amountOK || assetOK {
		t.Fatalf("expected rejection of a payment from an unexpected source account, got amount=%v asset=%v", amountOK, assetOK)
	}
}

// TestVerifyOps_LookAlikeIssuerIsRejected: a token sharing the "USDC" code
// but issued by a different (unofficial/fake) issuer must not satisfy asset
// verification just because the code matches.
func TestVerifyOps_LookAlikeIssuerIsRejected(t *testing.T) {
	expected := usdcExpected("100.0000000", "0")
	ops := []operations.Operation{
		mustPaymentOp(t, platformFrom, platformTo, "100.0000000", "credit_alphanum4", "USDC", fakeIssuer),
	}

	amountOK, assetOK, _, _ := verifyOps(ops, expected)
	if amountOK || assetOK {
		t.Fatalf("expected rejection of a look-alike asset with the right code but wrong issuer, got amount=%v asset=%v", amountOK, assetOK)
	}
}

// TestVerifyOps_SplitAcrossOperationsIsRejected reproduces the exact #97
// vulnerability shape: one unrelated operation satisfies the amount, a
// different unrelated operation satisfies the asset, but no single operation
// satisfies both (plus source/destination) together. The old implementation
// tracked amountVerified/assetVerified independently across the whole
// operation list and would have accepted this.
func TestVerifyOps_SplitAcrossOperationsIsRejected(t *testing.T) {
	expected := usdcExpected("100.0000000", "0")
	ops := []operations.Operation{
		// Same (correct) destination as the real payment, right amount, but
		// the wrong asset — e.g. an XLM payment that happens to carry the
		// expected numeric amount.
		mustPaymentOp(t, platformFrom, platformTo, "100.0000000", "native", "", ""),
		// Same (correct) destination, right asset, but the wrong amount.
		mustPaymentOp(t, platformFrom, platformTo, "1.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	amountOK, assetOK, _, details := verifyOps(ops, expected)
	if amountOK || assetOK {
		t.Fatalf("expected rejection when amount and asset only match on different operations, got amount=%v asset=%v details=%q", amountOK, assetOK, details)
	}
}

// TestVerifyOps_FeePaidToAttackerIsRejected: the main leg is legitimate, but
// the "fee" payment goes to an attacker-controlled account instead of the
// platform fee wallet — feeVerified must stay false even though the overall
// amount moved matches tx.Amount (netAmount + divertedFee).
func TestVerifyOps_FeePaidToAttackerIsRejected(t *testing.T) {
	expected := usdcExpected("95.0000000", "5.0000000")
	ops := []operations.Operation{
		mustPaymentOp(t, platformFrom, platformTo, "95.0000000", "credit_alphanum4", "USDC", realIssuer),
		mustPaymentOp(t, platformFrom, attackerAddr, "5.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	_, _, feeOK, _ := verifyOps(ops, expected)
	if feeOK {
		t.Fatal("expected feeVerified=false when the fee-sized payment goes to an unexpected wallet")
	}
}

// TestVerifyOps_UnrelatedOperationTypeIsIgnored ensures a non-payment
// operation in the same transaction (e.g. a sponsored create_account) is
// simply skipped rather than causing a false match or a panic.
func TestVerifyOps_UnrelatedOperationTypeIsIgnored(t *testing.T) {
	expected := usdcExpected("100.0000000", "0")
	ops := []operations.Operation{
		mustNonPaymentOp(t, "create_account"),
		mustPaymentOp(t, platformFrom, platformTo, "100.0000000", "credit_alphanum4", "USDC", realIssuer),
	}

	amountOK, assetOK, feeOK, _ := verifyOps(ops, expected)
	if !amountOK || !assetOK || !feeOK {
		t.Fatalf("expected the legitimate payment op to still verify alongside an unrelated op, got amount=%v asset=%v fee=%v", amountOK, assetOK, feeOK)
	}
}

func TestVerifyOps_NativeXLMRequiresNativeType(t *testing.T) {
	expected := expectedPayment{
		FromPublicKey: platformFrom,
		ToPublicKey:   platformTo,
		AssetCode:     "XLM",
		NetAmount:     decimal.RequireFromString("50.0000000"),
		Fee:           decimal.Zero,
	}

	// A credit asset that happens to be coded "XLM" must not be treated as
	// native — only asset_type == "native" identifies the real network asset.
	ops := []operations.Operation{
		mustPaymentOp(t, platformFrom, platformTo, "50.0000000", "credit_alphanum4", "XLM", fakeIssuer),
	}

	amountOK, assetOK, _, _ := verifyOps(ops, expected)
	if amountOK || assetOK {
		t.Fatal("expected rejection of a non-native credit asset merely coded \"XLM\"")
	}

	nativeOps := []operations.Operation{
		mustPaymentOp(t, platformFrom, platformTo, "50.0000000", "native", "", ""),
	}
	amountOK, assetOK, feeOK, _ := verifyOps(nativeOps, expected)
	if !amountOK || !assetOK || !feeOK {
		t.Fatal("expected a true native XLM payment to verify")
	}
}

func TestVerifyOps_NoSourceWalletSkipsSourceCheck(t *testing.T) {
	// Indexer-discovered deposits have no internal FromWallet, so any source
	// account is acceptable — only the destination, asset, and amount matter.
	expected := expectedPayment{
		ToPublicKey: platformTo,
		AssetCode:   "XLM",
		NetAmount:   decimal.RequireFromString("10.0000000"),
		Fee:         decimal.Zero,
	}

	ops := []operations.Operation{
		mustPaymentOp(t, "GEXTERNALDEPOSITORXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX", platformTo, "10.0000000", "native", "", ""),
	}

	amountOK, assetOK, feeOK, _ := verifyOps(ops, expected)
	if !amountOK || !assetOK || !feeOK {
		t.Fatal("expected a deposit from an unconstrained external source to verify against the destination/asset/amount alone")
	}
}
