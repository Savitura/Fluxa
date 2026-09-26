# Fluxa Quickstart

This guide walks through the complete integration flow — from creating an account to receiving a webhook notification. Every step includes the exact `curl` command, the expected response, and what to watch out for.

> **Prerequisites**: You need `curl` and `jq` installed. Run Fluxa locally using Docker or from source (see the main [README](../README.md)).

---

## Step 1: Register an account and get a JWT

Create an account. Fluxa is multi-tenant — this creates a tenant (individual or organization) and returns a JWT for subsequent requests.

```bash
curl -X POST http://localhost:3000/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "name": "My Fintech Co",
    "email": "dev@myfintech.co",
    "password": "your-secure-password"
  }'
```

**Expected response (201 Created):**
```json
{
  "user": {
    "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d3f",
    "email": "dev@myfintech.co",
    "name": "My Fintech Co",
    "created_at": "2026-06-22T12:00:00Z"
  },
  "tenant": {
    "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d40",
    "name": "My Fintech Co",
    "created_at": "2026-06-22T12:00:00Z"
  },
  "role": "owner",
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs..."
}
```

**What could go wrong:**
- `400 BAD_REQUEST` — Missing or invalid fields. Ensure `name`, `email`, and `password` are all present.
- `409 Conflict` — Email already registered. Use a different email or proceed to login.

Save the `access_token` — you'll use it as `Authorization: Bearer <access_token>` in the next step. The `refresh_token` lets you mint a new access token without re-entering credentials:

```bash
curl -X POST http://localhost:3000/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "dev@myfintech.co",
    "password": "your-secure-password"
  }'
```

Login accepts `{ "email", "password" }` and returns the same `access_token` / `refresh_token` shape. To refresh, POST `{ "refresh_token": "<refresh_token>" }` to `/v1/auth/refresh`.

---

## Step 2: Create an API key

API keys are the primary authentication mechanism for programmatic access. The raw key is shown **exactly once** on creation.

```bash
curl -X POST http://localhost:3000/v1/keys \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <jwt_token>" \
  -d '{
    "label": "My Integration Key"
  }'
```

**Expected response (201 Created):**
```json
{
  "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d43",
  "key": "YOUR_API_KEY_sk_live_replace_with_real_key",
  "prefix": "sk_live_",
  "label": "My Integration Key",
  "created_at": "2026-06-22T12:00:00Z"
}
```

> **⚠️ Save the `key` value now.** It will never be returned again. If you lose it, revoke it and create a new one.

**What could go wrong:**
- `401` — JWT expired or missing. Re-login or re-register.
- `500 INTERNAL_ERROR` — Server error. Retry with exponential backoff.

For the remaining steps, use the API key directly:

```
Authorization: Bearer YOUR_API_KEY_sk_live_replace_with_real_key
```

---

## Step 3: Create a sending wallet

Create a Stellar wallet. Fluxa generates a keypair and stores the secret key encrypted with AES-256-GCM. The raw secret is never exposed.

```bash
curl -X POST http://localhost:3000/v1/wallets \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk_live_..." \
  -H "Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000"
```

> Mutating requests require a unique `Idempotency-Key` header (UUID v4) — replaying the same key returns the original result instead of creating a duplicate. Generate a fresh UUID for every new wallet, transfer, or conversion.

**Expected response (201 Created):**
```json
{
  "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d3f",
  "public_key": "GAIH3YPEXB6HHVH6RIC6LDGAB7G4WMFP7F2F3I4K6Z6Q5V7X5Y5Z5A5B",
  "created_at": "2026-06-22T12:00:00Z"
}
```

**What could go wrong:**
- `401` — Missing or invalid API key. Ensure the `Authorization` header is present.
- `500 INTERNAL_ERROR` — Keypair generation or encryption failure. Retry.

Save the `id` (wallet UUID) and `public_key` (Stellar address `G...`).

---

## Step 4: Fund it on testnet via Friendbot

On Stellar testnet, new accounts need an initial XLM balance to exist on the network. Use the Stellar Friendbot to fund the wallet.

```bash
curl "https://friendbot.stellar.org?addr=GAIH3YPEXB6HHVH6RIC6LDGAB7G4WMFP7F2F3I4K6Z6Q5V7X5Y5Z5A5B"
```

**Expected response (200 OK):**
```json
{
  "hash": "a1b2c3d4e5f6...",
  "_embedded": {
    "record": {
      "account": "GAIH3YPEXB6HHVH6RIC6LDGAB7G4WMFP7F2F3I4K6Z6Q5V7X5Y5Z5A5B",
      "balance": "10000.0000000"
    }
  }
}
```

**What could go wrong:**
- Friendbot returns an error — The account may already be funded. Friendbot can only fund an account once.
- Network timeout — Friendbot is rate-limited. Wait a few seconds and retry.

Verify the balance:

```bash
curl -H "Authorization: Bearer sk_live_..." \
  "http://localhost:3000/v1/wallets/<wallet_id>/balances"
```

Expected:
```json
{
  "wallet_id": "0193b0b4-...",
  "balances": [
    {
      "asset_code": "XLM",
      "balance": "10000.0000000"
    }
  ]
}
```

> **On mainnet**, skip Friendbot. Fund the account by sending XLM from an exchange or an existing wallet.

---

## Step 5: Add a USDC trustline

Before the wallet can hold USDC, it must establish a trustline to the USDC issuer. This submits a Stellar `change_trust` operation.

```bash
curl -X POST http://localhost:3000/v1/wallets/<wallet_id>/trustlines \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk_live_..." \
  -H "Idempotency-Key: 6ba7b810-9dad-11d1-80b4-00c04fd430c8" \
  -d '{
    "asset_code": "USDC",
    "issuer": "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5"
  }'
```

**Expected response (201 Created):**
```json
{
  "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d44",
  "asset_code": "USDC",
  "issuer": "GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5",
  "status": "added",
  "created_at": "2026-06-22T12:00:00Z"
}
```

**What could go wrong:**
- `400 BAD_REQUEST` — Invalid asset code or issuer. Use the correct testnet USDC issuer.
- `500 INTERNAL_ERROR` — Stellar submission failed. The account may not be funded (go back to Step 4).
- `op_already_exists` — Trustline already exists. This is harmless.

Now the wallet can hold USDC. Transfer some test USDC from the Stellar testnet friendbot or a faucet to this wallet, or proceed with XLM for transfers.

---

## Step 6: Get an FX quote

Before converting currencies, get a 30-second exchange rate quote. This locks in the rate for a short window.

```bash
curl -X POST http://localhost:3000/v1/fx/quote \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk_live_..." \
  -d '{
    "from_asset": "USDC",
    "to_asset": "NGN",
    "amount": "100.00"
  }'
```

**Expected response (200 OK):**
```json
{
  "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d45",
  "org_id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d40",
  "from_asset": "USDC",
  "to_asset": "NGN",
  "from_amount": "100",
  "to_amount": "150000",
  "rate": "1500",
  "fee": "0.50",
  "expires_at": "2026-06-22T12:00:30Z",
  "used": false
}
```

**What could go wrong:**
- `400 BAD_REQUEST` — Unsupported currency pair. Check that both assets are supported.
- `400 BAD_REQUEST` — `fee schedule not found`. Contact support to configure your fee schedule.

The `expires_at` field gives you 30 seconds to execute the conversion — pass the returned `id` as `quote_id` in the next step. If the quote expires (or was already used), you'll need to request a new one.

---

## Step 7: Execute the conversion

Convert using the quote from Step 6. Pass the quote's `id` as `quote_id` — the server validates the quote hasn't expired and hasn't been used before executing the swap. Both IDs must be UUIDs.

```bash
curl -X POST http://localhost:3000/v1/fx/convert \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk_live_..." \
  -H "Idempotency-Key: 7c9e6679-7425-40de-944b-e07fc1f90ae7" \
  -d '{
    "wallet_id": "550e8400-e29b-41d4-a716-446655440000",
    "quote_id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d45"
  }'
```

**Expected response (200 OK):**
```json
{
  "id": "conv-uuid",
  "wallet_id": "550e8400-e29b-41d4-a716-446655440000",
  "source_asset": "USDC",
  "dest_asset": "NGN",
  "source_amount": "0.0666666",
  "dest_amount": "100.0000000",
  "fee_amount": "0.0000333",
  "fee_bps": 50,
  "rate": "1500.0000000",
  "tx_hash": "a1b2c3d4e5f67890...",
  "created_at": "2026-06-22T12:00:00Z"
}
```

**What could go wrong:**
- `400 BAD_REQUEST` — Missing or non-UUID `wallet_id` / `quote_id`.
- `400 IDEMPOTENCY_KEY_REQUIRED` — Missing or invalid `Idempotency-Key` header. Send a fresh UUID v4.
- `422 QUOTE_EXPIRED` — The quote expired (30s TTL) or was already used. Get a new quote and try again.
- `400 BAD_REQUEST` — `insufficient balance`. The wallet doesn't have enough USDC.
- `400 BAD_REQUEST` — `invalid or unsupported asset`. Verify the asset code.

---

## Step 8: Initiate a transfer

Transfers are **asynchronous**. The API returns `202 Accepted` immediately with a `pending` status. A background worker submits the transaction to the Stellar network.

First, create a **second wallet** (recipient) by repeating Step 3, and fund it with XLM via Friendbot (Step 4).

```bash
curl -X POST http://localhost:3000/v1/transfers \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk_live_..." \
  -H "Idempotency-Key: 123e4567-e89b-42d3-a456-426614174000" \
  -d '{
    "from_wallet_id": "<sender_wallet_id>",
    "to_wallet_id": "<recipient_wallet_id>",
    "asset": "USDC",
    "amount": "10.50"
  }'
```

**Expected response (202 Accepted):**
```json
{
  "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d41",
  "tx_hash": "",
  "type": "transfer",
  "status": "pending",
  "from_wallet_id": "0193b0b4-...",
  "to_wallet_id": "0193b0b4-...",
  "asset": "USDC",
  "amount": "10.5000000",
  "fee_amount": "0.0300000",
  "net_amount": "10.4700000",
  "fee_bps": 30,
  "created_at": "2026-06-22T12:00:00Z"
}
```

**What could go wrong:**
- `400 BAD_REQUEST` — `source and destination wallets must differ`. Use two different wallets.
- `400 BAD_REQUEST` — `insufficient balance`. The sender wallet doesn't have enough USDC.
- `400 BAD_REQUEST` — `invalid or unsupported asset`. Use `USDC` or another supported asset.
- `400 BAD_REQUEST` — `fee schedule not found`. Contact support.
- `400 IDEMPOTENCY_KEY_REQUIRED` — Missing or invalid `Idempotency-Key` header. Send a fresh UUID v4.

> The `fee_amount` is the platform fee deducted from the transfer. `net_amount` is what the recipient receives.

---

## Step 9: Poll for status

Poll the transfer endpoint until the status changes from `pending` to `confirmed` or `failed`.

```bash
curl -H "Authorization: Bearer sk_live_..." \
  "http://localhost:3000/v1/transfers/<transfer_id>"
```

**Expected response — pending (200 OK):**
```json
{
  "id": "0193b0b4-...",
  "status": "pending",
  ...other fields...
}
```

**Expected response — confirmed (200 OK):**
```json
{
  "id": "0193b0b4-...",
  "status": "confirmed",
  "tx_hash": "a1b2c3d4e5f67890...",
  ...other fields...
}
```

**Expected response — failed (200 OK):**
```json
{
  "id": "0193b0b4-...",
  "status": "failed",
  ...other fields...
}
```

A simple polling loop in bash:

```bash
#!/bin/bash
ID="<transfer_id>"
KEY="sk_live_..."
URL="http://localhost:3000/v1/transfers/$ID"
STATUS="pending"
while [ "$STATUS" = "pending" ] || [ "$STATUS" = "submitted" ]; do
  sleep 2
  STATUS=$(curl -s -H "Authorization: Bearer $KEY" "$URL" | jq -r '.status')
  echo "Status: $STATUS"
done
echo "Final status: $STATUS"
```

**What could go wrong:**
- `404 NOT_FOUND` — Wrong transfer ID or the transfer belongs to another tenant.
- Transfer stays `pending` indefinitely — The worker may not be running or the Stellar network is slow. Check worker logs and Stellar status.

---

## Step 10: Register a webhook to receive `transfer.settled`

Instead of polling, register a webhook endpoint that Fluxa will call when a transfer settles. The payload includes an HMAC-SHA256 signature for verification.

```bash
curl -X POST http://localhost:3000/v1/webhooks \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk_live_..." \
  -d '{
    "url": "https://your-app.com/webhooks/fluxa",
    "events": [
      "transfer.settled",
      "transfer.failed"
    ]
  }'
```

**Expected response (201 Created):**
```json
{
  "id": "0193b0b4-1b33-7e9a-bcf6-2e2a0abb6d42",
  "url": "https://your-app.com/webhooks/fluxa",
  "secret": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
  "events": [
    "transfer.settled",
    "transfer.failed"
  ],
  "active": true,
  "created_at": "2026-06-22T12:00:00Z"
}
```

> **⚠️ Save the `secret`.** It is returned exactly once. You'll need it to verify webhook signatures.

**Verifying webhook signatures:**

Each webhook delivery includes a signature header:

```
X-Fluxa-Signature: sha256=<HMAC-SHA256 hex>
X-Fluxa-Event: transfer.settled
```

Verify using the secret:

```bash
# Example verification in Node.js
echo '
const crypto = require("crypto");

function verifyWebhook(payload, signature, secret) {
  const expected = "sha256=" + crypto
    .createHmac("sha256", secret)
    .update(JSON.stringify(payload))
    .digest("hex");
  return crypto.timingSafeEqual(Buffer.from(signature), Buffer.from(expected));
}
' | node
```

**What could go wrong:**
- `400 BAD_REQUEST` — Invalid URL format. Ensure the URL is valid and publicly reachable.
- Webhook deliveries show `failed` status — Your endpoint is not reachable or returned a non-2xx status. Check your server logs and firewall.

Check delivery status:

```bash
curl -H "Authorization: Bearer sk_live_..." \
  "http://localhost:3000/v1/webhooks/<webhook_id>/deliveries"
```

---

## Available event types

| Event | When fired |
|---|---|
| `transfer.initiated` | Transfer created and queued |
| `transfer.settled` | Transfer confirmed on Stellar |
| `transfer.failed` | Transfer failed |
| `wallet.funded` | Wallet received funds |
| `conversion.completed` | FX conversion executed |

---

## Next steps

- Import the [Postman collection](fluxa.postman_collection.json) to explore all endpoints interactively.
- Review the [Error Reference](errors.md) for a complete list of error codes and resolutions.
- Set up both the **API** (`cmd/api`) and **Worker** (`cmd/worker`) processes for local development.
