# Idempotency Keys

## What they are, and why they matter

Any client that calls a state-mutating Fluxa endpoint can lose the response to a
network error or timeout without knowing whether the request actually
succeeded server-side. Retrying blindly risks double-processing — e.g. a
second `POST /v1/transfers` that moves the same funds twice.

An idempotency key breaks that ambiguity. The client generates a unique key
once per *logical* operation and sends it as the `X-Idempotency-Key` (or `Idempotency-Key`) header.
Fluxa remembers the outcome of the first request under that key for 24 hours;
any retry with the same key and the same request body gets back the exact
same response, byte for byte, without the operation running again. This is
the same model used by Stripe, Adyen, and other payment processors.

## Endpoints that support idempotency keys

| Method | Path | Header | Note |
|---|---|---|---|
| `POST` | `/v1/transfers` | `X-Idempotency-Key` or `Idempotency-Key` | Optional, prevents duplicate transfers |
| `POST` | `/v1/withdrawals` | `X-Idempotency-Key` or `Idempotency-Key` | Optional, prevents duplicate withdrawals |
| `POST` | `/v1/wallets/:id/withdraw` | `X-Idempotency-Key` or `Idempotency-Key` | Optional |
| `POST` | `/v1/transfers/batch` | `Idempotency-Key` or `X-Idempotency-Key` | Batch payments |
| `POST` | `/v1/fx/convert` | `Idempotency-Key` or `X-Idempotency-Key` | FX conversions |
| `POST` | `/v1/wallets` | `Idempotency-Key` or `X-Idempotency-Key` | Wallet creation |
| `POST` | `/v1/wallets/:id/trustlines` | `Idempotency-Key` or `X-Idempotency-Key` | Trustline configuration |

Read-only endpoints (e.g. `GET /v1/transfers/:id`) never require or use the header.

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

## Retry strategy

- Use exponential backoff with jitter between retries (e.g. `base * 2^attempt + random_jitter`).
- Cap retries at **5 attempts**.
- **Never rotate the idempotency key on retry.** Rotating it defeats the
  entire mechanism — the server will see it as a brand-new operation and
  execute it again.
- If a retry returns `409 REQUEST_IN_PROGRESS`, back off and retry the same
  key later — the original request is still being processed.

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

| Code | HTTP status | Meaning |
|---|---|---|
| `IDEMPOTENCY_KEY_REQUIRED` | 400 | The `Idempotency-Key` header was missing on an endpoint that requires it. |
| `REQUEST_IN_PROGRESS` | 409 | A request with this key is already being processed; retry later. |
| `IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY` | 409 | This key was already used with a different request body — generate a new key for a genuinely different operation. |

## How it works server-side

Each request is fingerprinted as `SHA-256(method + path + body)`. The first
request for a given `(org, key)` pair claims the key and proceeds to the
handler; the response is cached against the key once the handler completes.
Any later request with the same key:

- while the original is still in flight → `409 REQUEST_IN_PROGRESS`
- after it completed, with the same body → the cached response, replayed exactly
- after it completed, with a different body → `409 IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY`

Records are scoped per organization and expire 24 hours after creation, after
which the same key can be reused for a new operation.
