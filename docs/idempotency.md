# Idempotency Keys

## What they are, and why they matter

Any client that calls a state-mutating Fluxa endpoint can lose the response to a
network error or timeout without knowing whether the request actually
succeeded server-side. Retrying blindly risks double-processing — e.g. a
second `POST /v1/transfers` that moves the same funds twice.

An idempotency key breaks that ambiguity. The client generates a unique key
once per _logical_ operation and sends it in the `Idempotency-Key` header.
`X-Idempotency-Key` is accepted as a legacy alias for the same value; sending
both headers with **different** values is rejected with `400`
(`IDEMPOTENCY_KEY_CONFLICT`) instead of letting one endpoint prefer one header
and another endpoint the other.
Fluxa remembers the outcome of the first request under that key for 24 hours
(configurable via `IDEMPOTENCY_TTL_HOURS`); any retry with the same key and the
same request body gets back the exact same response, byte for byte, without the
operation running again. This is the same model used by Stripe, Adyen, and other
payment processors.

## Endpoints that support idempotency keys

| Method | Path                               | Header                                   | Note                                                                            |
| ------ | ---------------------------------- | ---------------------------------------- | ------------------------------------------------------------------------------- |
| `POST` | `/v1/transfers`                    | `X-Idempotency-Key` or `Idempotency-Key` | Optional, prevents duplicate transfers                                          |
| `POST` | `/v1/withdrawals`                  | `X-Idempotency-Key` or `Idempotency-Key` | Optional, prevents duplicate withdrawals                                        |
| `POST` | `/v1/wallets/:id/withdraw`         | `X-Idempotency-Key` or `Idempotency-Key` | Optional                                                                        |
| `POST` | `/v1/transfers/batch`              | `Idempotency-Key` or `X-Idempotency-Key` | Required; retries with the same key and body replay the original batch response |
| `POST` | `/v1/fx/convert`                   | `Idempotency-Key` or `X-Idempotency-Key` | FX conversions                                                                  |
| `POST` | `/v1/wallets`                      | `Idempotency-Key` or `X-Idempotency-Key` | Wallet creation                                                                 |
| `POST` | `/v1/wallets/:id/trustlines`       | `Idempotency-Key` or `X-Idempotency-Key` | Trustline configuration                                                         |
| `POST` | `/v1/claimable-balances`           | `Idempotency-Key` or `X-Idempotency-Key` | Claimable balance creation                                                      |
| `POST` | `/v1/claimable-balances/:id/claim` | `Idempotency-Key` or `X-Idempotency-Key` | Claimable balance claiming                                                      |

Read-only endpoints (e.g. `GET /v1/transfers/:id`) never require or use the header.

## Header rules

| Sent by the client | Result |
| --- | --- |
| `Idempotency-Key: k` | Used as-is (canonical header). |
| `X-Idempotency-Key: k` | Accepted as an alias; the value is used as the key. |
| Both, same value | Accepted; the canonical value is used. |
| Both, different values | `400` `IDEMPOTENCY_KEY_CONFLICT` — resolve the conflict client-side. |
| Neither, on an endpoint that requires a key | `400` `IDEMPOTENCY_KEY_REQUIRED`. |

The header is resolved in one place (`internal/server/idempotency`), so every
endpoint behaves the same way; handlers read the key from the request context
rather than from the headers themselves.

## Generating a key

Clients can provide a **UUID v4** or any unique client reference string (e.g. `tx_order_987213`). Non-UUID strings are deterministically transformed into a RFC 4122 compliant UUID using SHA-256:

```bash
# macOS / Linux
uuidgen

# Python
python3 -c "import uuid; print(uuid.uuid4())"

# Node.js
node -e "console.log(require('crypto').randomUUID())"
```

Generate the key **once** when the operation is first attempted, and reuse
that same key for every retry of that operation — never generate a new one
per HTTP attempt.

## Retry strategy & recommendation: inspect rather than re-key

When a client encounters a network partition, client-side timeout, deploy interruption, or dropped connection, **inspect the key rather than generating a new key**. Generating a new key risks duplicating a transfer or mutating state, while giving up forces manual reconciliation.

- **Inspect first**: Call `GET /v1/idempotency/{key}` to check whether the original request is `processing`, `completed`, `failed`, or `unknown`.
- If `completed` or `failed`, the original response body and status code are faithfully recovered without re-executing the operation.
- If `processing`, back off using the provided `retry_after_seconds` (or `Retry-After` header) and retry or re-inspect later.
- If `unknown`, the operation never reached the platform or has expired past its retention TTL; it is safe to proceed.
- **Never rotate the idempotency key on retry.** Rotating it defeats deduplication — the server will see it as a brand-new operation and execute it again.
- Use exponential backoff with jitter between retries (e.g. `base * 2^attempt + random_jitter`), capped at **5 attempts**.

## Key inspection endpoint (`GET /v1/idempotency/{key}`)

Clients can query any previously used idempotency key:

```bash
curl -X GET https://api.fluxa.example/v1/idempotency/tx_order_987213 \
  -H "Authorization: Bearer $TOKEN"
```

### Response format

```json
{
  "key": "b477c770-4f51-40be-a006-03daef194d30",
  "status": "completed",
  "created_at": "2026-09-30T00:15:00Z",
  "expires_at": "2026-10-01T00:15:00Z",
  "request_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "response": {
    "status": 200,
    "headers": {
      "Content-Type": ["application/json"]
    },
    "body": {
      "id": "tr_9a8b7c6d",
      "status": "completed"
    },
    "body_truncated": false,
    "body_size_bytes": 48
  }
}
```

### Key states

| Status | Meaning |
| ------ | ------- |
| `completed` | The operation finished successfully (`status < 400`). Cached response status, headers, and body are faithfully returned. |
| `failed` | The operation completed with an error (`status >= 400`). Cached error response is returned without re-execution. |
| `processing` | The request is currently being handled. The response includes `retry_after_seconds`, `retry_hint`, and a `Retry-After` HTTP header. |
| `unknown` | The key was never seen for this organization or its retention TTL has elapsed. |

### Request hash verification

Clients can pass `?request_hash=<hash>` (or `X-Request-Hash` header) to verify that an existing key corresponds to a specific request body:
- If the hash matches, the record is returned with status 200.
- If the hash mismatches, the endpoint returns `422 IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY`, confirming that the key was previously used for a different payload.

### Guarantees

- **Tenant-scoped**: Keys are strictly isolated per organization and mode (live/test). Looking up another tenant's key returns `status: "unknown"` with HTTP 200, preventing cross-tenant existence oracles.
- **Read-only**: Inspection never acquires locks or leases, never mutates state, and never completes in-flight requests.
- **Rate limited**: Subject to standard tenant rate limits.
- **Size bounded**: Stored response bodies are capped at **64 KB** in the inspection payload; larger responses are safely truncated with `body_truncated: true` and `body_size_bytes` indicating total size.
- **Retention**: Records match the platform's configured TTL (`IDEMPOTENCY_TTL_HOURS`, default 24h). Expired records return `status: "unknown"`.



## Example

```bash
curl -X POST https://api.fluxa.example/v1/transfers \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-Idempotency-Key: $(uuidgen)" \
  -d '{
    "from_wallet_id": "b3f1...",
    "to_wallet_id": "9ac2...",
    "asset": "USDC",
    "amount": "100.00"
  }'
```

Retrying the exact same request (same key, same body) returns the original
response — no duplicate transfer is created.

## Error codes

| Code                                         | HTTP status | Meaning                                                                                                           |
| -------------------------------------------- | ----------- | ----------------------------------------------------------------------------------------------------------------- |
| `IDEMPOTENCY_KEY_REQUIRED`                   | 400         | The `Idempotency-Key` header was missing on an endpoint that requires it.                                         |
| `REQUEST_IN_PROGRESS`                        | 409         | A request with this key is already being processed; retry later.                                                  |
| `IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY` | 409         | This key was already used with a different request body — generate a new key for a genuinely different operation. |

## How it works server-side

Each request is fingerprinted as `SHA-256(method + path + body)`. The first
request for a given `(org, key)` pair claims the key and proceeds to the
handler; the response is cached against the key once the handler completes.
Any later request with the same key:

- while the original is still in flight → `409 REQUEST_IN_PROGRESS`
- after it completed, with the same body → the cached response, replayed exactly
- after it completed, with a different body → `409 IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY`

Records are scoped per organization and expire after the configured TTL
(default 24 hours), after which the same key can be reused for a new operation.

## TTL and cleanup

The TTL for idempotency records defaults to **24 hours** and is controlled by
the `IDEMPOTENCY_TTL_HOURS` environment variable (minimum: 1 hour). Setting a
shorter TTL reduces storage but also shrinks the replay window — clients must
complete all retries within the configured window.

### Background cleanup job

The worker process runs a cleanup goroutine every **hour** that removes all
rows whose `expires_at` is in the past. Deletes are issued in batches of 1 000
rows to avoid long-held locks or I/O spikes. On startup the worker immediately
drains any backlog accumulated during downtime.

The `idempotency_records` table has an index on `expires_at`
(`idx_idempotency_records_expires_at`, added in migration
`20260927000000`) so the batch delete uses an index scan rather than a
sequential scan.

### Per-key opportunistic delete

`TryAcquire` also issues a targeted `DELETE … WHERE org_id = $1 AND key = $2
AND expires_at <= NOW()` on the hot path when a key-reuse attempt hits an
expired row blocking the unique index. This is deliberately kept alongside the
background sweep:

- It clears the conflict immediately on the request path, so a client reusing a
  key after its TTL gets a fresh response without waiting for the next hourly
  sweep.
- It is scoped to a single `(org_id, key)` pair and never performs a
  table-wide scan, so it has no impact on vacuum or I/O for unrelated rows.

The background job covers the common case (keys used once, never retried) where
the opportunistic delete never fires.

## Data retention

Every idempotency record stores a `request_hash` (a SHA-256 digest of the
method, path, and request body) and a `response_body` (the serialized HTTP
response). This data:

- Is retained for the duration of the TTL (`IDEMPOTENCY_TTL_HOURS`, default 24h).
- Is deleted by the background cleanup job once `expires_at` has elapsed.
- Should be considered in any organization-level data-retention or right-to-erasure policy,
  since `response_body` may contain transaction IDs and amounts.
