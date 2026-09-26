# Fluxa webhook signature verification (Ruby).
# No external dependencies — uses the `openssl` standard library only.

require "openssl"

TOLERANCE_SECONDS = 300

VerifyResult = Struct.new(:valid, :reason)

# Constant-time string comparison. `OpenSSL.secure_compare` isn't guaranteed
# to exist across every openssl gem version, so this is hand-rolled instead —
# still no external dependency, and it never short-circuits: the XOR
# accumulation always walks every byte regardless of where (or whether) the
# strings first differ, so comparison time can't leak how many leading bytes
# matched, which is exactly what an attacker could otherwise exploit to
# forge a valid signature byte-by-byte.
def constant_time_compare(a, b)
  return false unless a.bytesize == b.bytesize

  result = 0
  a.bytes.each_with_index { |byte, i| result |= byte ^ b.bytes[i] }
  result.zero?
end

# Verifies a Fluxa webhook delivery.
#
# secret:    the webhook endpoint's signing secret.
# timestamp: value of the X-Fluxa-Timestamp header (Unix seconds, as a string).
# body:      the raw, unparsed request body exactly as received.
# signature: value of the X-Fluxa-Signature header, e.g. "sha256=...".
def verify_webhook_signature(secret, timestamp, body, signature)
  timestamp_seconds = Integer(timestamp, exception: false)
  return VerifyResult.new(false, "invalid_timestamp") if timestamp_seconds.nil?

  # Reject deliveries older (or newer) than the tolerance window — this is
  # what stops a captured payload from being replayed later.
  now_seconds = Time.now.to_i
  return VerifyResult.new(false, "stale_timestamp") if (now_seconds - timestamp_seconds).abs >= TOLERANCE_SECONDS

  signed_payload = "#{timestamp}.#{body}"
  digest = OpenSSL::HMAC.hexdigest(OpenSSL::Digest.new("sha256"), secret, signed_payload)
  expected = "sha256=#{digest}"

  return VerifyResult.new(false, "signature_mismatch") unless constant_time_compare(expected, signature)

  VerifyResult.new(true, nil)
end

# Usage:
#   result = verify_webhook_signature(
#     webhook_secret,
#     request.headers["X-Fluxa-Timestamp"],
#     raw_body, # must be the raw body string, not a re-serialized hash
#     request.headers["X-Fluxa-Signature"],
#   )
#   halt 400, result.reason unless result.valid
