/**
 * Copy-paste snippets shown by the "Verify Signature" dashboard tool.
 * These must stay functionally identical to the canonical reference
 * implementations in docs/webhook-verification/ — this file exists only
 * because the dashboard can't read files out of docs/ at runtime.
 */

export const WEBHOOK_VERIFICATION_SNIPPETS: Record<string, { label: string; code: string }> = {
  typescript: {
    label: 'TypeScript',
    code: `import { createHmac, timingSafeEqual } from "node:crypto";

const TOLERANCE_SECONDS = 300;

function verifyWebhookSignature(secret: string, timestamp: string, body: string, signature: string) {
  const timestampSeconds = Number(timestamp);
  if (!Number.isFinite(timestampSeconds)) return { valid: false, reason: "invalid_timestamp" };

  const nowSeconds = Math.floor(Date.now() / 1000);
  if (Math.abs(nowSeconds - timestampSeconds) >= TOLERANCE_SECONDS) {
    return { valid: false, reason: "stale_timestamp" };
  }

  const signedPayload = \`\${timestamp}.\${body}\`;
  const expected = "sha256=" + createHmac("sha256", secret).update(signedPayload, "utf8").digest("hex");

  const provided = Buffer.from(signature, "utf8");
  const expectedBuf = Buffer.from(expected, "utf8");
  if (provided.length !== expectedBuf.length || !timingSafeEqual(provided, expectedBuf)) {
    return { valid: false, reason: "signature_mismatch" };
  }
  return { valid: true, reason: null };
}`,
  },
  python: {
    label: 'Python',
    code: `import hashlib
import hmac
import time

TOLERANCE_SECONDS = 300


def verify_webhook_signature(secret, timestamp, body, signature):
    try:
        timestamp_seconds = int(timestamp)
    except (TypeError, ValueError):
        return {"valid": False, "reason": "invalid_timestamp"}

    now_seconds = int(time.time())
    if abs(now_seconds - timestamp_seconds) >= TOLERANCE_SECONDS:
        return {"valid": False, "reason": "stale_timestamp"}

    signed_payload = f"{timestamp}.{body}"
    expected = "sha256=" + hmac.new(
        secret.encode("utf-8"), signed_payload.encode("utf-8"), hashlib.sha256
    ).hexdigest()

    if not hmac.compare_digest(expected, signature):
        return {"valid": False, "reason": "signature_mismatch"}
    return {"valid": True, "reason": None}`,
  },
  go: {
    label: 'Go',
    code: `func VerifyWebhookSignature(secret, timestamp, body, signature string) (bool, string) {
	timestampSeconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false, "invalid_timestamp"
	}

	delta := time.Now().Unix() - timestampSeconds
	if delta < 0 {
		delta = -delta
	}
	if delta >= 300 {
		return false, "stale_timestamp"
	}

	signedPayload := timestamp + "." + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return false, "signature_mismatch"
	}
	return true, ""
}`,
  },
  ruby: {
    label: 'Ruby',
    code: `require "openssl"

TOLERANCE_SECONDS = 300

def constant_time_compare(a, b)
  return false unless a.bytesize == b.bytesize
  result = 0
  a.bytes.each_with_index { |byte, i| result |= byte ^ b.bytes[i] }
  result.zero?
end

def verify_webhook_signature(secret, timestamp, body, signature)
  timestamp_seconds = Integer(timestamp, exception: false)
  return { valid: false, reason: "invalid_timestamp" } if timestamp_seconds.nil?

  now_seconds = Time.now.to_i
  return { valid: false, reason: "stale_timestamp" } if (now_seconds - timestamp_seconds).abs >= TOLERANCE_SECONDS

  signed_payload = "#{timestamp}.#{body}"
  digest = OpenSSL::HMAC.hexdigest(OpenSSL::Digest.new("sha256"), secret, signed_payload)
  expected = "sha256=#{digest}"

  return { valid: false, reason: "signature_mismatch" } unless constant_time_compare(expected, signature)
  { valid: true, reason: nil }
end`,
  },
  php: {
    label: 'PHP',
    code: `function verify_webhook_signature(string $secret, string $timestamp, string $body, string $signature): array {
    if (!ctype_digit(ltrim($timestamp, '-')) || $timestamp === '' || $timestamp === '-') {
        return ['valid' => false, 'reason' => 'invalid_timestamp'];
    }
    $timestampSeconds = (int) $timestamp;

    if (abs(time() - $timestampSeconds) >= 300) {
        return ['valid' => false, 'reason' => 'stale_timestamp'];
    }

    $signedPayload = $timestamp . '.' . $body;
    $expected = 'sha256=' . hash_hmac('sha256', $signedPayload, $secret);

    if (!hash_equals($expected, $signature)) {
        return ['valid' => false, 'reason' => 'signature_mismatch'];
    }
    return ['valid' => true, 'reason' => null];
}`,
  },
};
