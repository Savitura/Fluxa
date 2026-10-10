package yellowcard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/shopspring/decimal"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func paymentCompletedPayload(id, ref, status, amount string) string {
	return `{
		"event": "payment.completed",
		"data": {
			"id": "` + id + `",
			"reference": "` + ref + `",
			"type": "payment",
			"status": "` + status + `",
			"amount": "` + amount + `",
			"currency": "NGN"
		}
	}`
}

func payoutCompletedPayload(id, ref, status, amount, currency string) string {
	return `{
		"event": "payout.completed",
		"data": {
			"id": "` + id + `",
			"reference": "` + ref + `",
			"type": "payout",
			"status": "` + status + `",
			"amount": "` + amount + `",
			"currency": "` + currency + `"
		}
	}`
}

func computeHMACSHA256(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func makeSignedHeaders(secret string, payload []byte) http.Header {
	h := make(http.Header)
	h.Set("x-yellowcard-signature", computeHMACSHA256(secret, payload))
	return h
}

// ─── signature verification ─────────────────────────────────────────────────

func TestHandleWebhook_ValidHMAC_Accepted(t *testing.T) {
	p := NewProvider("api-key", "webhook-secret", true)
	payload := []byte(paymentCompletedPayload("yc-1", "REF-1", "completed", "5000"))
	headers := makeSignedHeaders("webhook-secret", payload)

	evt, err := p.HandleWebhook(context.Background(), payload, headers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
}

func TestHandleWebhook_InvalidHMAC_Rejected(t *testing.T) {
	p := NewProvider("api-key", "webhook-secret", true)
	payload := []byte(paymentCompletedPayload("yc-1", "REF-1", "completed", "5000"))

	h := make(http.Header)
	h.Set("x-yellowcard-signature", "deadbeefdeadbeef")

	_, err := p.HandleWebhook(context.Background(), payload, h)
	if err == nil {
		t.Fatal("expected error for invalid HMAC, got nil")
	}
}

func TestHandleWebhook_NoWebhookKey_SkipsVerification(t *testing.T) {
	// Yellow Card provider with no webhookKey skips signature verification
	// (matches the current production behaviour: an empty key means
	// verification is disabled for that deployment).
	p := NewProvider("api-key", "", true)
	payload := []byte(paymentCompletedPayload("yc-1", "REF-1", "completed", "5000"))
	headers := make(http.Header) // no signature header

	_, err := p.HandleWebhook(context.Background(), payload, headers)
	if err != nil {
		t.Fatalf("expected success when webhookKey is empty, got: %v", err)
	}
}

// ─── event type mapping ──────────────────────────────────────────────────────

func TestHandleWebhook_PaymentCompleted_MapsToDepositConfirmed(t *testing.T) {
	p := NewProvider("api-key", "", true)
	payload := []byte(paymentCompletedPayload("yc-1", "REF-1", "completed", "5000"))

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Type != fiat.EventDepositConfirmed {
		t.Errorf("expected type %s, got %s", fiat.EventDepositConfirmed, evt.Type)
	}
}

func TestHandleWebhook_PaymentFailed_MapsToDepositFailed(t *testing.T) {
	p := NewProvider("api-key", "", true)
	payload := []byte(`{
		"event": "payment.failed",
		"data": {
			"id": "yc-2",
			"reference": "REF-2",
			"type": "payment",
			"status": "failed",
			"amount": "5000",
			"currency": "NGN"
		}
	}`)

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Type != fiat.EventDepositFailed {
		t.Errorf("expected type %s, got %s", fiat.EventDepositFailed, evt.Type)
	}
}

func TestHandleWebhook_PayoutCompleted_MapsToWithdrawalSent(t *testing.T) {
	p := NewProvider("api-key", "", true)
	payload := []byte(payoutCompletedPayload("yc-3", "REF-3", "completed", "5000", "NGN"))

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Type != fiat.EventWithdrawalSent {
		t.Errorf("expected type %s, got %s", fiat.EventWithdrawalSent, evt.Type)
	}
}

func TestHandleWebhook_PayoutFailed_MapsToWithdrawalFailed(t *testing.T) {
	p := NewProvider("api-key", "", true)
	payload := []byte(`{
		"event": "payout.failed",
		"data": {
			"id": "yc-4",
			"reference": "REF-4",
			"type": "payout",
			"status": "failed",
			"amount": "5000",
			"currency": "NGN"
		}
	}`)

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Type != fiat.EventWithdrawalFailed {
		t.Errorf("expected type %s, got %s", fiat.EventWithdrawalFailed, evt.Type)
	}
}

// ─── status mapping ──────────────────────────────────────────────────────────

func TestHandleWebhook_CompletedStatus_MapsToCompleted(t *testing.T) {
	for _, status := range []string{"completed", "sent", "successful"} {
		t.Run("status="+status, func(t *testing.T) {
			p := NewProvider("api-key", "", true)
			payload := []byte(paymentCompletedPayload("yc-1", "REF-1", status, "5000"))

			evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
			if err != nil {
				t.Fatalf("unexpected error for status %q: %v", status, err)
			}
			if evt.Status != "completed" {
				t.Errorf("expected status completed for provider status %q, got %s", status, evt.Status)
			}
		})
	}
}

func TestHandleWebhook_OtherStatus_MapsToFailed(t *testing.T) {
	// Any status that is not completed/sent/successful maps to "failed" in
	// the current Yellow Card implementation.
	p := NewProvider("api-key", "", true)
	payload := []byte(paymentCompletedPayload("yc-1", "REF-1", "rejected", "5000"))

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Status != "failed" {
		t.Errorf("expected status failed for unknown provider status, got %s", evt.Status)
	}
}

// ─── reference extraction ────────────────────────────────────────────────────

func TestHandleWebhook_ReferencePreferredOverID(t *testing.T) {
	// When both "id" and "reference" are present, "reference" is the
	// merchant-supplied value and should be used as ProviderRef.
	p := NewProvider("api-key", "", true)
	payload := []byte(paymentCompletedPayload("yc-id-123", "MERCHANT-REF-999", "completed", "5000"))

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.ProviderRef != "MERCHANT-REF-999" {
		t.Errorf("expected ProviderRef MERCHANT-REF-999, got %s", evt.ProviderRef)
	}
}

func TestHandleWebhook_FallsBackToIDWhenReferenceEmpty(t *testing.T) {
	p := NewProvider("api-key", "", true)
	payload := []byte(`{
		"event": "payment.completed",
		"data": {
			"id": "yc-id-only",
			"reference": "",
			"type": "payment",
			"status": "completed",
			"amount": "5000",
			"currency": "NGN"
		}
	}`)

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When reference is empty, ProviderRef falls back to data.id
	if evt.ProviderRef != "yc-id-only" {
		t.Errorf("expected ProviderRef yc-id-only, got %s", evt.ProviderRef)
	}
}

// ─── amount parsing ──────────────────────────────────────────────────────────

func TestHandleWebhook_Amount_ParsedExactly(t *testing.T) {
	p := NewProvider("api-key", "", true)
	payload := []byte(paymentCompletedPayload("yc-1", "REF-1", "completed", "1500.50"))

	evt, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := decimal.RequireFromString("1500.50")
	if !evt.Amount.Equal(want) {
		t.Errorf("expected amount %s, got %s", want, evt.Amount)
	}
}

// ─── replay idempotency ──────────────────────────────────────────────────────

func TestHandleWebhook_ReplayedPayload_ParsesIdentically(t *testing.T) {
	// Provider layer is stateless — the same payload must parse to the same
	// event both times so the caller's event-ID deduplication can work.
	p := NewProvider("api-key", "", true)
	payload := []byte(paymentCompletedPayload("yc-evt-42", "REF-42", "completed", "5000"))

	first, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error on first delivery: %v", err)
	}
	second, err := p.HandleWebhook(context.Background(), payload, make(http.Header))
	if err != nil {
		t.Fatalf("unexpected error on replayed delivery: %v", err)
	}

	if first.ProviderRef != second.ProviderRef || first.Type != second.Type || first.Status != second.Status {
		t.Errorf("replayed delivery parsed differently: %+v vs %+v", first, second)
	}
}

// ─── malformed payload ───────────────────────────────────────────────────────

func TestHandleWebhook_MalformedJSON_Rejected(t *testing.T) {
	p := NewProvider("api-key", "", true)

	_, err := p.HandleWebhook(context.Background(), []byte(`{not valid json`), make(http.Header))
	if err == nil {
		t.Fatal("expected error for malformed JSON, got nil")
	}
}

// ─── supported currencies / countries ───────────────────────────────────────

func TestSupportedCurrencies_ContainsExpected(t *testing.T) {
	p := NewProvider("api-key", "", true)
	currencies := p.SupportedCurrencies()
	want := map[string]bool{"NGN": false, "GHS": false, "KES": false, "UGX": false, "TZS": false, "ZAR": false, "ZMW": false}
	for _, c := range currencies {
		want[c] = true
	}
	for currency, found := range want {
		if !found {
			t.Errorf("expected currency %s to be supported", currency)
		}
	}
}

func TestSupportedCountries_ContainsExpected(t *testing.T) {
	p := NewProvider("api-key", "", true)
	countries := p.SupportedCountries()
	want := map[string]bool{"NG": false, "GH": false, "KE": false, "UG": false, "TZ": false, "ZA": false, "ZM": false}
	for _, c := range countries {
		want[c] = true
	}
	for country, found := range want {
		if !found {
			t.Errorf("expected country %s to be supported", country)
		}
	}
}

// ─── sandbox/live URL ───────────────────────────────────────────────────────

func TestNewProvider_SandboxURL(t *testing.T) {
	p := NewProvider("key", "secret", true)
	if p.baseURL != "https://sandbox-api.yellowcard.io" {
		t.Errorf("expected sandbox URL, got %s", p.baseURL)
	}
}

func TestNewProvider_LiveURL(t *testing.T) {
	p := NewProvider("key", "secret", false)
	if p.baseURL != "https://api.yellowcard.io" {
		t.Errorf("expected live URL, got %s", p.baseURL)
	}
}
