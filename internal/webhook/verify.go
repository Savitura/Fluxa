package webhook

import (
	"crypto/hmac"
	"strconv"
	"time"
)

const verifyToleranceSeconds = 300

// VerifyResult is the outcome of verifying one webhook delivery signature.
type VerifyResult struct {
	Valid  bool
	Reason string // empty when Valid is true
}

// Verify checks a webhook delivery's signature using the same algorithm
// documented for developers in docs/webhook-verification — this is the
// backend's own implementation of that same contract, not a separate one,
// so POST /v1/webhooks/verify and the reference snippets can never drift
// out of sync with each other.
func Verify(secret, timestamp, body, signature string) VerifyResult {
	timestampSeconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return VerifyResult{Valid: false, Reason: "invalid_timestamp"}
	}

	delta := time.Now().Unix() - timestampSeconds
	if delta < 0 {
		delta = -delta
	}
	if delta >= verifyToleranceSeconds {
		return VerifyResult{Valid: false, Reason: "stale_timestamp"}
	}

	expected := sign(secret, timestamp, []byte(body))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return VerifyResult{Valid: false, Reason: "signature_mismatch"}
	}

	return VerifyResult{Valid: true}
}
