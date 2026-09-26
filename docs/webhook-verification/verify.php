<?php
// Fluxa webhook signature verification (PHP).
// No external dependencies — uses the `hash` extension (bundled with PHP) only.

declare(strict_types=1);

const FLUXA_TOLERANCE_SECONDS = 300;

final class VerifyResult
{
    public function __construct(
        public readonly bool $valid,
        public readonly ?string $reason,
    ) {
    }
}

/**
 * Verifies a Fluxa webhook delivery.
 *
 * @param string $secret    The webhook endpoint's signing secret.
 * @param string $timestamp Value of the X-Fluxa-Timestamp header (Unix seconds, as a string).
 * @param string $body      The raw, unparsed request body exactly as received.
 * @param string $signature Value of the X-Fluxa-Signature header, e.g. "sha256=...".
 */
function verify_webhook_signature(string $secret, string $timestamp, string $body, string $signature): VerifyResult
{
    if (!ctype_digit(ltrim($timestamp, '-')) || $timestamp === '' || $timestamp === '-') {
        return new VerifyResult(false, 'invalid_timestamp');
    }
    $timestampSeconds = (int) $timestamp;

    // Reject deliveries older (or newer) than the tolerance window — this is
    // what stops a captured payload from being replayed later.
    $nowSeconds = time();
    if (abs($nowSeconds - $timestampSeconds) >= FLUXA_TOLERANCE_SECONDS) {
        return new VerifyResult(false, 'stale_timestamp');
    }

    $signedPayload = $timestamp . '.' . $body;
    $expected = 'sha256=' . hash_hmac('sha256', $signedPayload, $secret);

    // hash_equals is a constant-time comparison: a plain `===`/`==` leaks
    // timing information proportional to how many leading bytes match,
    // which an attacker can use to forge a valid signature byte-by-byte.
    if (!hash_equals($expected, $signature)) {
        return new VerifyResult(false, 'signature_mismatch');
    }

    return new VerifyResult(true, null);
}

// Usage:
//   $result = verify_webhook_signature(
//       $webhookSecret,
//       $_SERVER['HTTP_X_FLUXA_TIMESTAMP'],
//       $rawBody, // must be the raw request body, not json_decode()'d and re-encoded
//       $_SERVER['HTTP_X_FLUXA_SIGNATURE'],
//   );
//   if (!$result->valid) {
//       http_response_code(400);
//       exit($result->reason);
//   }
