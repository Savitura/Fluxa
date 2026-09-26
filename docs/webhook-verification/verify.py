# Fluxa webhook signature verification (Python).
# No external dependencies — uses the `hmac` and `hashlib` standard library
# modules only.

import hashlib
import hmac
import time
from dataclasses import dataclass
from typing import Optional

TOLERANCE_SECONDS = 300


@dataclass
class VerifyResult:
    valid: bool
    reason: Optional[str]


def verify_webhook_signature(
    secret: str, timestamp: str, body: str, signature: str
) -> VerifyResult:
    """Verifies a Fluxa webhook delivery.

    secret:    the webhook endpoint's signing secret.
    timestamp: value of the X-Fluxa-Timestamp header (Unix seconds, as a string).
    body:      the raw, unparsed request body exactly as received.
    signature: value of the X-Fluxa-Signature header, e.g. "sha256=...".
    """
    try:
        timestamp_seconds = int(timestamp)
    except (TypeError, ValueError):
        return VerifyResult(valid=False, reason="invalid_timestamp")

    # Reject deliveries older (or newer) than the tolerance window — this is
    # what stops a captured payload from being replayed later.
    now_seconds = int(time.time())
    if abs(now_seconds - timestamp_seconds) >= TOLERANCE_SECONDS:
        return VerifyResult(valid=False, reason="stale_timestamp")

    signed_payload = f"{timestamp}.{body}"
    expected = "sha256=" + hmac.new(
        secret.encode("utf-8"), signed_payload.encode("utf-8"), hashlib.sha256
    ).hexdigest()

    # hmac.compare_digest is a constant-time comparison: a plain `==` leaks
    # timing information proportional to how many leading characters match,
    # which an attacker can use to forge a valid signature byte-by-byte.
    if not hmac.compare_digest(expected, signature):
        return VerifyResult(valid=False, reason="signature_mismatch")

    return VerifyResult(valid=True, reason=None)


# Usage:
#   result = verify_webhook_signature(
#       webhook_secret,
#       request.headers["X-Fluxa-Timestamp"],
#       raw_body,  # must be the raw body bytes/string, not a re-serialized dict
#       request.headers["X-Fluxa-Signature"],
#   )
#   if not result.valid:
#       return Response(status=400, body=result.reason)
