// Fluxa webhook signature verification (TypeScript / Node.js).
// No external dependencies — uses Node's built-in `crypto` module only.

import { createHmac, timingSafeEqual } from "node:crypto";

const TOLERANCE_SECONDS = 300;

export interface VerifyResult {
  valid: boolean;
  reason: string | null;
}

/**
 * Verifies a Fluxa webhook delivery.
 *
 * @param secret     The webhook endpoint's signing secret.
 * @param timestamp  Value of the `X-Fluxa-Timestamp` header (Unix seconds, as a string).
 * @param body       The raw, unparsed request body exactly as received.
 * @param signature  Value of the `X-Fluxa-Signature` header, e.g. "sha256=...".
 */
export function verifyWebhookSignature(
  secret: string,
  timestamp: string,
  body: string,
  signature: string,
): VerifyResult {
  const timestampSeconds = Number(timestamp);
  if (!Number.isFinite(timestampSeconds)) {
    return { valid: false, reason: "invalid_timestamp" };
  }

  // Reject deliveries older (or newer) than the tolerance window — this is
  // what stops a captured payload from being replayed later.
  const nowSeconds = Math.floor(Date.now() / 1000);
  if (Math.abs(nowSeconds - timestampSeconds) >= TOLERANCE_SECONDS) {
    return { valid: false, reason: "stale_timestamp" };
  }

  const signedPayload = `${timestamp}.${body}`;
  const expected =
    "sha256=" + createHmac("sha256", secret).update(signedPayload, "utf8").digest("hex");

  const provided = Buffer.from(signature, "utf8");
  const expectedBuf = Buffer.from(expected, "utf8");

  // Constant-time comparison: a plain `===`/`==` leaks timing information
  // proportional to how many leading bytes match, which an attacker can use
  // to forge a valid signature byte-by-byte. timingSafeEqual takes the same
  // time regardless of where (or whether) the buffers first differ — but it
  // throws if the buffers have different lengths, which itself would leak
  // information via the exception path, so the length check happens first
  // and short-circuits to an equally content-free "signature_mismatch".
  if (provided.length !== expectedBuf.length || !timingSafeEqual(provided, expectedBuf)) {
    return { valid: false, reason: "signature_mismatch" };
  }

  return { valid: true, reason: null };
}

// Usage:
//   const result = verifyWebhookSignature(
//     webhookSecret,
//     request.headers["x-fluxa-timestamp"],
//     rawBody, // must be the raw string body, not JSON.parse()'d and re-stringified
//     request.headers["x-fluxa-signature"],
//   );
//   if (!result.valid) return response.status(400).send(result.reason);
