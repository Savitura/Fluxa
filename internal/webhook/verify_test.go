package webhook

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestVerify_ValidSignature(t *testing.T) {
	secret := "whsec_test"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"event":"transfer.settled"}`
	sig := sign(secret, timestamp, []byte(body))

	result := Verify(secret, timestamp, body, sig)
	if !result.Valid {
		t.Fatalf("expected valid, got reason=%q", result.Reason)
	}
	if result.Reason != "" {
		t.Fatalf("expected empty reason on success, got %q", result.Reason)
	}
}

func TestVerify_StaleTimestamp(t *testing.T) {
	secret := "whsec_test"
	timestamp := strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10)
	body := `{"event":"transfer.settled"}`
	sig := sign(secret, timestamp, []byte(body))

	result := Verify(secret, timestamp, body, sig)
	if result.Valid {
		t.Fatal("expected a 6-minute-old delivery to be rejected as stale")
	}
	if result.Reason != "stale_timestamp" {
		t.Fatalf("reason = %q, want stale_timestamp", result.Reason)
	}
}

func TestVerify_BoundaryJustInsideTolerance(t *testing.T) {
	secret := "whsec_test"
	timestamp := strconv.FormatInt(time.Now().Add(-299*time.Second).Unix(), 10)
	body := `{"event":"transfer.settled"}`
	sig := sign(secret, timestamp, []byte(body))

	result := Verify(secret, timestamp, body, sig)
	if !result.Valid {
		t.Fatalf("299s old delivery should still be valid, got reason=%q", result.Reason)
	}
}

func TestVerify_BoundaryAtTolerance(t *testing.T) {
	secret := "whsec_test"
	timestamp := strconv.FormatInt(time.Now().Add(-300*time.Second).Unix(), 10)
	body := `{"event":"transfer.settled"}`
	sig := sign(secret, timestamp, []byte(body))

	result := Verify(secret, timestamp, body, sig)
	if result.Valid {
		t.Fatal("a delivery exactly at the 300s tolerance boundary must be rejected")
	}
	if result.Reason != "stale_timestamp" {
		t.Fatalf("reason = %q, want stale_timestamp", result.Reason)
	}
}

func TestVerify_InvalidTimestamp(t *testing.T) {
	result := Verify("whsec_test", "not-a-number", "{}", "sha256=deadbeef")
	if result.Valid {
		t.Fatal("expected a non-numeric timestamp to be rejected")
	}
	if result.Reason != "invalid_timestamp" {
		t.Fatalf("reason = %q, want invalid_timestamp", result.Reason)
	}
}

func TestVerify_SignatureMismatch(t *testing.T) {
	secret := "whsec_test"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"event":"transfer.settled"}`

	result := Verify(secret, timestamp, body, "sha256=0000000000000000000000000000000000000000000000000000000000000000")
	if result.Valid {
		t.Fatal("expected a mismatched signature to be rejected")
	}
	if result.Reason != "signature_mismatch" {
		t.Fatalf("reason = %q, want signature_mismatch", result.Reason)
	}
}

func TestVerify_WrongSecretRejected(t *testing.T) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"event":"transfer.settled"}`
	sig := sign("correct-secret", timestamp, []byte(body))

	result := Verify("wrong-secret", timestamp, body, sig)
	if result.Valid {
		t.Fatal("expected verification with the wrong secret to fail")
	}
	if result.Reason != "signature_mismatch" {
		t.Fatalf("reason = %q, want signature_mismatch", result.Reason)
	}
}

func TestVerify_TamperedBodyRejected(t *testing.T) {
	secret := "whsec_test"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	sig := sign(secret, timestamp, []byte(`{"amount":"10.00"}`))

	// Same secret, same timestamp, same signature — but the body an
	// attacker actually delivered differs from what was signed.
	result := Verify(secret, timestamp, `{"amount":"10000.00"}`, sig)
	if result.Valid {
		t.Fatal("expected a tampered body to fail verification even with a signature that was valid for the original body")
	}
	if result.Reason != "signature_mismatch" {
		t.Fatalf("reason = %q, want signature_mismatch", result.Reason)
	}
}

func TestVerify_TestVectors(t *testing.T) {
	data, err := os.ReadFile("../../docs/webhook-verification/test-vectors.json")
	if err != nil {
		t.Fatalf("failed to read test vectors: %v", err)
	}

	var payload struct {
		ReferenceNow     int64 `json:"referenceNow"`
		ToleranceSeconds int64 `json:"toleranceSeconds"`
		Vectors          []struct {
			Name              string `json:"name"`
			Secret            string `json:"secret"`
			Timestamp         string `json:"timestamp"`
			Body              string `json:"body"`
			ExpectedSignature string `json:"expectedSignature"`
			ExpectStale       bool   `json:"expectStale"`
		} `json:"vectors"`
	}

	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("failed to unmarshal test vectors: %v", err)
	}

	origNow := nowFunc
	defer func() { nowFunc = origNow }()
	nowFunc = func() time.Time {
		return time.Unix(payload.ReferenceNow, 0)
	}

	for _, v := range payload.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			res := Verify(v.Secret, v.Timestamp, v.Body, v.ExpectedSignature)
			if v.ExpectStale {
				if res.Valid {
					t.Fatal("expected stale timestamp, got valid")
				}
				if res.Reason != "stale_timestamp" {
					t.Fatalf("expected reason stale_timestamp, got %q", res.Reason)
				}
			} else {
				if !res.Valid {
					t.Fatalf("expected valid signature, got invalid (reason: %q)", res.Reason)
				}
			}
		})
	}
}
