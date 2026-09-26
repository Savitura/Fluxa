// Package webhookverify verifies Fluxa webhook deliveries.
// No external dependencies — uses the standard library only.
package webhookverify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

const toleranceSeconds = 300

// VerifyResult is the outcome of verifying one webhook delivery.
type VerifyResult struct {
	Valid  bool
	Reason string // empty when Valid is true
}

// VerifyWebhookSignature verifies a Fluxa webhook delivery.
//
// secret:    the webhook endpoint's signing secret.
// timestamp: value of the X-Fluxa-Timestamp header (Unix seconds, as a string).
// body:      the raw, unparsed request body exactly as received.
// signature: value of the X-Fluxa-Signature header, e.g. "sha256=...".
func VerifyWebhookSignature(secret, timestamp, body, signature string) (VerifyResult, error) {
	timestampSeconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return VerifyResult{Valid: false, Reason: "invalid_timestamp"}, nil
	}

	// Reject deliveries older (or newer) than the tolerance window — this is
	// what stops a captured payload from being replayed later.
	now := time.Now().Unix()
	delta := now - timestampSeconds
	if delta < 0 {
		delta = -delta
	}
	if delta >= toleranceSeconds {
		return VerifyResult{Valid: false, Reason: "stale_timestamp"}, nil
	}

	signedPayload := timestamp + "." + body
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(signedPayload)); err != nil {
		return VerifyResult{}, errors.New("failed to compute signature")
	}
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// hmac.Equal is a constant-time comparison: a plain `==` leaks timing
	// information proportional to how many leading bytes match, which an
	// attacker can use to forge a valid signature byte-by-byte.
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return VerifyResult{Valid: false, Reason: "signature_mismatch"}, nil
	}

	return VerifyResult{Valid: true}, nil
}

// Usage:
//   result, err := webhookverify.VerifyWebhookSignature(
//       webhookSecret,
//       r.Header.Get("X-Fluxa-Timestamp"),
//       string(rawBody), // must be the raw body bytes, not re-marshaled JSON
//       r.Header.Get("X-Fluxa-Signature"),
//   )
//   if err != nil || !result.Valid {
//       http.Error(w, result.Reason, http.StatusBadRequest)
//       return
//   }
