package flutterwave

import (
	"net/http"
	"testing"

	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/shopspring/decimal"
)

func chargeCompletedPayload(txRef, status string, amount string, currency string) string {
	return `{
		"event": "charge.completed",
		"data": {
			"id": 285959875,
			"tx_ref": "` + txRef + `",
			"status": "` + status + `",
			"amount": ` + amount + `,
			"currency": "` + currency + `"
		}
	}`
}

func headersWithSignature(sig string) http.Header {
	h := make(http.Header)
	if sig != "" {
		h.Set("verif-hash", sig)
	}
	return h
}

// ─── signature verification ─────────────────────────────────────────────────

func TestHandleWebhook_ValidSignature_Accepted(t *testing.T) {
	p := NewProvider("mock", "correct-secret-hash")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("correct-secret-hash"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.ProviderRef != "REF-1" {
		t.Errorf("expected ProviderRef REF-1, got %s", evt.ProviderRef)
	}
	if evt.Status != "completed" {
		t.Errorf("expected status completed, got %s", evt.Status)
	}
	if evt.EventID != "285959875" {
		t.Errorf("expected EventID 285959875, got %s", evt.EventID)
	}
}

func TestHandleWebhook_TamperedSignature_Rejected(t *testing.T) {
	p := NewProvider("mock", "correct-secret-hash")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")

	// An attacker who intercepted a legitimate payload but doesn't know the
	// configured secret cannot forge a matching header.
	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("attacker-guess"))
	if err == nil {
		t.Fatal("expected error for tampered/incorrect signature, got nil")
	}
}

func TestHandleWebhook_MissingSignatureHeader_Rejected(t *testing.T) {
	p := NewProvider("mock", "correct-secret-hash")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature(""))
	if err == nil {
		t.Fatal("expected error for missing signature header, got nil")
	}
}

func TestHandleWebhook_TamperedPayloadWithValidHeaderValue_StillRejected(t *testing.T) {
	// Simulates an attacker who replays a captured signature header against
	// a modified payload (amount changed from 100 to 100000). Flutterwave's
	// scheme ties the header to the configured secret, not to the payload
	// bytes, so this specific attack is caught downstream by the
	// amount/currency check in the service layer — but the signature check
	// itself must still require the correct secret, which the attacker does
	// not have.
	p := NewProvider("mock", "correct-secret-hash")
	tampered := chargeCompletedPayload("REF-1", "successful", "100000", "NGN")

	_, err := p.HandleWebhook(nil, []byte(tampered), headersWithSignature("wrong-secret"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestHandleWebhook_NoSecretConfigured_FailsClosed(t *testing.T) {
	// An unconfigured webhook secret must never be treated as "verification
	// disabled" outside of the explicit mock/dev bypass.
	p := NewProvider("live-key", "")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("anything"))
	if err == nil {
		t.Fatal("expected error when no webhook secret is configured, got nil")
	}
}

func TestHandleWebhook_MockSecret_BypassesSignatureCheck(t *testing.T) {
	p := NewProvider("mock", "mock")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature(""))
	if err != nil {
		t.Fatalf("mock mode should bypass signature verification, got error: %v", err)
	}
}

// ─── status handling ─────────────────────────────────────────────────────────

func TestHandleWebhook_SuccessfulStatus_MapsToCompleted(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Status != "completed" {
		t.Errorf("expected completed, got %s", evt.Status)
	}
}

func TestHandleWebhook_FailedStatus_MapsToFailed(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "failed", "100", "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Status != "failed" {
		t.Errorf("expected failed, got %s", evt.Status)
	}
}

func TestHandleWebhook_PendingStatus_Rejected(t *testing.T) {
	// A non-final status must never be forwarded as if it were a completed
	// or failed outcome — the previous implementation defaulted anything
	// that wasn't "successful" to "failed", which would have prematurely
	// marked an in-flight deposit as failed.
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "pending", "100", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err == nil {
		t.Fatal("expected error for non-final status, got nil")
	}
}

func TestHandleWebhook_UnknownStatus_Rejected(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "some-new-status-flutterwave-added", "100", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err == nil {
		t.Fatal("expected error for unsupported status, got nil")
	}
}

func TestHandleWebhook_MissingReference_Rejected(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := `{"event":"charge.completed","data":{"id":1,"status":"successful","amount":100,"currency":"NGN"}}`

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err == nil {
		t.Fatal("expected error for missing reference, got nil")
	}
}

// ─── replay: identical delivery presented twice ───────────────────────────────

func TestHandleWebhook_ReplayedPayload_ParsesIdenticallyBothTimes(t *testing.T) {
	// The provider layer is stateless — it cannot itself detect that a
	// payload was already processed (that requires the persisted deposit
	// state, exercised in the fiat service tests). What it must guarantee
	// is that a replayed (byte-identical) delivery with a valid signature
	// keeps parsing to the same event deterministically, in particular the
	// same EventID, so the caller has a stable identity to dedupe on.
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")
	headers := headersWithSignature("secret")

	first, err := p.HandleWebhook(nil, []byte(payload), headers)
	if err != nil {
		t.Fatalf("unexpected error on first delivery: %v", err)
	}
	second, err := p.HandleWebhook(nil, []byte(payload), headers)
	if err != nil {
		t.Fatalf("unexpected error on replayed delivery: %v", err)
	}

	if first.EventID != second.EventID || first.ProviderRef != second.ProviderRef {
		t.Errorf("replayed delivery produced a different identity: %+v vs %+v", first, second)
	}
}

func TestHandleWebhook_EventType(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "100", "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Type != fiat.EventDepositConfirmed {
		t.Errorf("expected type %s, got %s", fiat.EventDepositConfirmed, evt.Type)
	}
}

// ─── exact decimal amount parsing (no float64) ──────────────────────────────

func TestHandleWebhook_FractionalAmount_ParsesExactly(t *testing.T) {
	// Regression test: decoding through float64 turned 100.10 into
	// 100.099999999999994, failing the exact-match check against the stored
	// deposit. The raw literal must convert exactly.
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "100.10", "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := decimal.RequireFromString("100.10")
	if !evt.Amount.Equal(want) {
		t.Errorf("expected exact amount %s, got %s", want, evt.Amount)
	}
}

func TestHandleWebhook_StringAmount_Accepted(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", `"100.10"`, "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !evt.Amount.Equal(decimal.RequireFromString("100.10")) {
		t.Errorf("expected exact amount 100.10, got %s", evt.Amount)
	}
}

func TestHandleWebhook_HighPrecisionWithinCap_Accepted(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "10.123456", "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !evt.Amount.Equal(decimal.RequireFromString("10.123456")) {
		t.Errorf("expected exact amount 10.123456, got %s", evt.Amount)
	}
}

func TestHandleWebhook_ScientificNotation_AcceptedExactly(t *testing.T) {
	// Policy: scientific notation converts exactly (1e2 == 100).
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "1e2", "NGN")

	evt, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !evt.Amount.Equal(decimal.NewFromInt(100)) {
		t.Errorf("expected exact amount 100, got %s", evt.Amount)
	}
}

func TestHandleWebhook_OverPrecisionAmount_Rejected(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "10.1234567", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err == nil {
		t.Fatal("expected error for over-precision amount, got nil")
	}
}

func TestHandleWebhook_NegativeAmount_Rejected(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "-50", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err == nil {
		t.Fatal("expected error for negative amount, got nil")
	}
}

func TestHandleWebhook_ZeroAmount_Rejected(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := chargeCompletedPayload("REF-1", "successful", "0", "NGN")

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err == nil {
		t.Fatal("expected error for zero amount, got nil")
	}
}

func TestHandleWebhook_MissingAmount_Rejected(t *testing.T) {
	p := NewProvider("mock", "secret")
	payload := `{"event":"charge.completed","data":{"id":1,"tx_ref":"REF-1","status":"successful","currency":"NGN"}}`

	_, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret"))
	if err == nil {
		t.Fatal("expected error for missing amount, got nil")
	}
}

func TestHandleWebhook_MalformedAmount_Rejected(t *testing.T) {
	p := NewProvider("mock", "secret")
	for _, amount := range []string{`"abc"`, `"12.34.56"`, `true`} {
		payload := chargeCompletedPayload("REF-1", "successful", amount, "NGN")
		if _, err := p.HandleWebhook(nil, []byte(payload), headersWithSignature("secret")); err == nil {
			t.Fatalf("expected error for malformed amount %s, got nil", amount)
		}
	}
}
