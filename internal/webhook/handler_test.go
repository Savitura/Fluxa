package webhook

import (
	"strconv"
	"testing"
	"time"
)

func TestVerify_RawBodyPrecisionAndNonTrivialPayload(t *testing.T) {
	secret := "whsec_test_non_trivial"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	// Non-trivial payload with unicode, special chars, whitespace
	body := `{"event":"transfer.settled","data":{"amount":"150.00","currency":"XLM","note":"Café résumé 🎉 \n\t \r \"escaped\""}}`
	sig := sign(secret, timestamp, []byte(body))

	result := Verify(secret, timestamp, body, sig)
	if !result.Valid {
		Fatalf := t.Fatalf
		Fatalf("expected valid signature for non-trivial payload, got reason=%q", result.Reason)
	}

	// Tampering with a single character in the payload should fail
	tamperedBody := `{"event":"transfer.settled","data":{"amount":"150.01","currency":"XLM","note":"Café résumé 🎉 \n\t \r \"escaped\""}}`
	tamperedResult := Verify(secret, timestamp, tamperedBody, sig)
	if tamperedResult.Valid {
		t.Fatal("expected tampered non-trivial payload to be rejected")
	}
}
