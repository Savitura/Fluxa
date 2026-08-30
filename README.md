# Fluxa
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT) [![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](http://makeapullrequest.com)


**Cross-border payment infrastructure for emerging markets.**

Fluxa is a programmable payments API built on the [Stellar](https://stellar.org) network. It gives fintech products and developers the primitives to move value across borders — wallet management, internal transfers, FX conversion via Stellar path payments, and settlement — behind a clean REST API.

[![Run in Postman](https://img.shields.io/badge/Run%20in%20Postman-FF6C37?style=for-the-badge&logo=postman&logoColor=white)](docs/fluxa.postman_collection.json)
[![Environment](https://img.shields.io/badge/Environment-FF6C37?style=for-the-badge&logo=postman&logoColor=white)](docs/fluxa.postman_environment.json)
[![Quickstart](https://img.shields.io/badge/Quickstart-36C5F0?style=for-the-badge&logo=readthedocs&logoColor=white)](docs/quickstart.md)
[![Errors](https://img.shields.io/badge/Error%20Reference-CD5C5C?style=for-the-badge&logo=readthedocs&logoColor=white)](docs/errors.md)

> **Status**: Active development — testnet only.

---

## What it does

- ✅ **Wallets** — create Stellar accounts with AES-256-GCM encrypted secrets; never expose raw keys
- ✅ **Transfers** — async payment submission with queue-backed retry and status polling
- ✅ **FX / Conversion** — quote and execute cross-asset swaps via Stellar DEX path payments
- ✅ **Settlement** — background worker submits transactions to Stellar, handles retries, confirms on-chain
- ✅ **Ledger indexer** — streams Horizon events to keep local state in sync
- ✅ **Multi-tenant** — API key + JWT auth; individual developers and business organizations each get scoped access
- ✅ **Webhooks** — signed delivery of payment events to developer endpoints
- 🔜 **Sandbox mode** — `sk_test_` keys route to Stellar testnet for safe integration testing
- ✅ **Recurring payouts** — scheduled transfers with daily/weekly/monthly cadence
- ✅ **Fiat rails** — deposit/withdrawal via Flutterwave integration
- ✅ **Organization management** — invite members, role-based access control (owner/admin/dev/viewer)

---

## Architecture

```
Client Applications
        │  Authorization: Bearer sk_live_... or sk_test_...
        ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Fluxa REST API                             │
│  Chi router │ JWT + API key auth │ Rate limiting │ Tenant scope │
└─────────────────────────────────────────────────────────────────┘
        │
        ├── Wallet Service       ──► postgres: wallets, balances
        ├── Transfer Service     ──► postgres: transactions
        ├── FX Service           ──► Stellar DEX + rate cache (Redis)
        ├── Fee Service          ──► postgres: fees, fee_collections
        └── Webhook Dispatcher   ──► postgres: webhook_endpoints, deliveries
                │
                ▼  (Asynq job queue)
        ┌────────────────────────────────────────┐
        │           Background Worker             │
        │  Settlement Engine │ Ledger Indexer     │
        │  Reconciliation    │ Scheduler          │
        └────────────────────────────────────────┘
                │
                ▼
        Stellar Network (Horizon API + Soroban RPC)
        testnet: horizon-testnet.stellar.org
        mainnet: horizon.stellar.org
```

**Two processes**

| Binary | Role |
|---|---|
| `cmd/api` | HTTP server — handles all REST requests, enqueues async work |
| `cmd/worker` | Asynq worker — settles transfers, runs ledger indexer, processes webhooks |

Transfers are **asynchronous**. `POST /v1/transfers` returns `202 Accepted` with a `pending` transaction immediately. Poll `GET /v1/transfers/:id` or receive a `transfer.settled` webhook for the final status. A transfer stopped by compliance screening comes back as `compliance_hold` instead of `pending` and is not settled until it is approved.

---

## Project Structure

```
fluxa/
├── cmd/
│   ├── api/main.go           # HTTP server entry point
│   └── worker/main.go        # Background worker entry point
├── internal/
│   ├── config/               # Viper env config
│   ├── domain/               # Core types: Wallet, Transaction, Conversion, errors
│   ├── crypto/               # AES-256-GCM encrypt/decrypt (stdlib only)
│   ├── assets/               # Asset registry: USDC/EURC issuers per network
│   ├── stellar/              # Horizon client, keypair generation, signer interface
│   ├── postgres/             # pgx/v5 repository implementations
│   ├── queue/                # Asynq client + task type definitions
│   ├── wallet/               # Wallet service + HTTP handler
│   ├── transfer/             # Transfer service + HTTP handler
│   ├── batch/                # Batch transfers + CSV export, reuses transfer settlement
│   ├── schedule/             # Recurring payouts + Asynq periodic task
│   ├── fx/                   # FX service + rate providers + HTTP handler
│   ├── fees/                 # Fee calculation and collection
│   ├── settlement/           # Settlement engine + Asynq task handler
│   ├── indexer/              # Ledger indexer + Asynq periodic task
│   ├── webhook/              # Webhook dispatcher + delivery worker
│   ├── reconcile/            # DB vs on-chain reconciliation
│   ├── apikey/               # API key generation, hashing, verification
│   ├── auth/                 # User registration, login, JWT
│   ├── org/                  # Organization members, roles
│   ├── fiat/                 # Fiat rail abstraction + provider adapters
│   ├── alerting/             # Alerting client for platform notifications
│   ├── tenant/               # Tenant context helpers
│   ├── server/               # Chi router setup, middleware
│   └── api/                  # Shared request validation + response helpers
└── db/
    └── migrations/           # golang-migrate SQL files (numbered up/down pairs)
```

---

## Getting Started

### Prerequisites

- Go 1.22+
- PostgreSQL 15+
- Redis 7+

The repository root is a Go backend workspace. It does not require root-level
Node.js, TypeScript, Prisma, or BullMQ tooling to build or run the API and
worker.

### 1. Clone and install

```bash
git clone https://github.com/Savitura/Fluxa
cd Fluxa
go mod tidy
```

### 2. Configure environment

```bash
cp .env.example .env
```

| Variable | Description |
|---|---|
| `PORT` | HTTP listen port (default: `3000`) |
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_URL` | Redis connection string |
| `STELLAR_NETWORK` | `testnet` or `mainnet` |
| `STELLAR_HORIZON_URL` | Horizon endpoint |
| `STELLAR_USDC_ISSUER` | USDC issuer public key |
| `MASTER_ENCRYPTION_KEY` | 64 hex chars (32 bytes) — encrypts stored wallet secrets |
| `PLATFORM_FEE_WALLET_PUBLIC_KEY` | Stellar address where platform fees are collected |
| `TREASURY_SECRET_KEY` | Stellar key that funds new accounts (testnet: leave empty, use Friendbot) |

Generate a master key:
```bash
openssl rand -hex 32
```

### 3. Run migrations

```bash
make migrate
```

### 4. Start the API and worker

```bash
# Terminal 1
make run-api

# Terminal 2
make run-worker
```

---

## API Reference

All endpoints are prefixed `/v1`. Auth: `Authorization: Bearer <api_key_or_jwt>`. Errors:

```json
{ "error": { "code": "WALLET_NOT_FOUND", "message": "wallet not found" } }
```

### Authentication

```http
POST /v1/auth/register     Create account (individual or organization)
POST /v1/auth/login        Login — returns JWT
POST /v1/auth/refresh      Refresh access token
POST /v1/keys              Create API key  →  sk_live_... or sk_test_...
GET  /v1/keys              List keys (prefix only, never raw)
DELETE /v1/keys/:id        Revoke key
```

### Wallets

```http
POST /v1/wallets           Create wallet — returns public key only
GET  /v1/wallets/:id/balances   Live balances from Horizon (all assets)
POST /v1/wallets/:id/trustlines  Add Stellar trustline for a new asset
```

### Transfers

```http
POST /v1/transfers         Initiate transfer (202 Accepted — async)
GET  /v1/transfers/:id     Poll status
GET  /v1/transfers         List (filter by wallet, status, date)
POST /v1/transfers/batch   Up to 100 transfers in one call
GET  /v1/transfers/batch/:batchId          Batch status with per-transfer breakdown
GET  /v1/transfers/batch/:batchId/export   CSV download of batch results
```

**Status flow:** `pending` → `confirmed` | `failed`

**Batch status** is derived live from its linked transactions: `pending` → `processing` → `partial` | `completed` | `failed`.

### Scheduled Payouts

```http
POST   /v1/schedules       Create a recurring transfer (daily | weekly | monthly)
GET    /v1/schedules       List schedules
PATCH  /v1/schedules/:id   Pause, resume, or update amount/frequency/end_date
DELETE /v1/schedules/:id   Cancel a schedule
```

A background worker checks for due schedules every minute and enqueues a normal transfer for each one — a paused schedule is skipped until resumed.

### FX

```http
POST /v1/fx/quote          Get a 30-second exchange rate quote
POST /v1/fx/convert        Execute a currency swap
GET  /v1/fx/rates          Live rates for a currency pair
```

### Webhooks

```http
POST   /v1/webhooks        Register endpoint + event subscriptions
GET    /v1/webhooks        List endpoints
DELETE /v1/webhooks/:id    Remove endpoint
GET    /v1/webhooks/:id/deliveries  Delivery log
```

**Event types:** `transfer.initiated` · `transfer.settled` · `transfer.failed` · `wallet.funded` · `conversion.completed` · `transfer.compliance_hold` · `transfer.compliance_approved` · `transfer.compliance_rejected` · `sanctions.refresh_failed`

### Fees

```http
GET /v1/fees               Your fee schedule (transfer/conversion fee rates)
```

### Organization

```http
POST /v1/org/members/invite     Invite member to organization
GET  /v1/org/members            List organization members
PATCH  /v1/org/members/{userId}   Update member role
DELETE /v1/org/members/{userId}   Remove member
POST /v1/org/invites/accept     Accept organization invite (public)
```

### Fiat Rails

```http
POST /v1/wallets/{id}/deposit/fiat   Initiate fiat deposit
POST /v1/wallets/{id}/withdraw/fiat  Initiate fiat withdrawal
POST /v1/webhooks/fiat/{provider}   Fiat provider webhook receiver
```

### Compliance (Owner & Admin only)

Every transfer is screened before it is enqueued for settlement. A blocked
transfer is refused with `403 TRANSFER_BLOCKED_SANCTIONS` and no transaction
is created; a held transfer is persisted as `compliance_hold` and waits for a
decision here.

```http
GET  /v1/admin/compliance/reviews              List held transfers (filter: ?status=pending)
GET  /v1/admin/compliance/reviews/{id}         Get a single review
POST /v1/admin/compliance/reviews/{id}/approve Release a held transfer for settlement
POST /v1/admin/compliance/reviews/{id}/reject  Reject a held transfer
GET  /v1/admin/compliance/sanctions-status     OFAC SDN list state and last refresh
```

Screening **fails closed**: if the sanctions list cannot be loaded, transfers
are held for review rather than cleared. Check `sanctions-status` if every
transfer is suddenly being held — `loaded: false` is the signal.

### Health

```http
GET /health                Health check
```

---

## Integration Resources

| Resource | Description |
|---|---|
| [Quickstart Guide](docs/quickstart.md) | End-to-end walkthrough from account creation to webhook delivery — with exact curl commands |
| [Postman Collection](docs/fluxa.postman_collection.json) | Pre-configured requests for every endpoint, organised by folder, with tests and pre-request scripts |
| [Postman Environment](docs/fluxa.postman_environment.json) | Testnet and mainnet environment variables — switch between targets with one click |
| [Error Reference](docs/errors.md) | Complete list of every error code, HTTP status, description, resolution, and example response |

```bash
# Import the collection via the Postman CLI (requires Postman CLI installed)
postman collection import docs/fluxa.postman_collection.json
postman environment import docs/fluxa.postman_environment.json
```

---

## Security

- **Key storage**: Stellar secrets are encrypted with AES-256-GCM before storage. The 32-byte master key lives only in env — never in the database or logs.
- **No key exposure**: Secret keys are never returned by any API endpoint.
- **Signer abstraction**: `stellar.Signer` in `internal/stellar/signer.go` isolates all signing. Swap `EnvSigner` for HSM or AWS KMS without touching the settlement engine.
- **Decimal arithmetic**: All monetary values use `shopspring/decimal` — no floating-point.
- **API key hashing**: Raw keys are SHA-256 hashed before storage; the plaintext is shown exactly once on creation.

---

## Development

```bash
make test          # go test ./... -race
make test-cover    # with HTML coverage report
make lint          # golangci-lint
make build         # outputs bin/api + bin/worker
make tidy          # go mod tidy
```

Fund a testnet wallet:
```bash
curl "https://friendbot.stellar.org?addr=<PUBLIC_KEY>"
```

---

## Part of Savitura

- **[CrowdPay](https://github.com/Savitura/crowdpay)** — crowdfunding platform built on top of Fluxa payment rails
- **[SaviTools](https://github.com/Savitura/Savitools)** — developer tools: API playground, transaction inspector, wallet sandbox

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT
