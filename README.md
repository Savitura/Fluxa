# Fluxa

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](http://makeapullrequest.com)

**Cross-border payment infrastructure for emerging markets.**

Fluxa is a programmable payments API built on [Stellar](https://stellar.org). It provides the primitives fintech products need to move value across borders: wallet management, internal transfers, FX conversion via Stellar path payments, and settlement — all behind a clean REST API.

## The Problem

Moving money across borders in emerging markets is slow, expensive, and opaque. Traditional rails charge 5-10% fees, take days to settle, and require manual reconciliation. Developers building fintech products in these regions have to either build payment infrastructure from scratch or accept the limitations of legacy providers.

## The Solution

Fluxa abstracts the complexity of cross-border payments into a simple API:

- **Custodial wallets** — Create Stellar wallets with encrypted key storage (AES-256-GCM)
- **Instant transfers** — Move funds between wallets with sub-second finality on Stellar
- **FX conversion** — Convert between currencies using Stellar path payments with transparent fees
- **Fiat on/off ramps** — Deposit and withdraw local currency via integrated providers
- **Compliance screening** — OFAC sanctions checking, velocity limits, and structuring detection
- **Webhooks** — Real-time notifications with HMAC-SHA256 signature verification

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Fluxa API                               │
│  cmd/api                                                        │
│  ├── REST endpoints (auth, wallets, transfers, FX, webhooks)   │
│  ├── JWT + API key authentication                               │
│  └── Idempotency middleware                                     │
└─────────────────────────────────────────────────────────────────┘
        │                                    │
        ▼                                    ▼
┌───────────────┐                   ┌───────────────────┐
│  PostgreSQL   │                   │      Redis        │
│  - Wallets    │                   │  - Job queue      │
│  - Transfers  │                   │  - Rate limiting  │
│  - Tenants    │                   │  - FX quote cache │
└───────────────┘                   └───────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Fluxa Worker                              │
│  cmd/worker                                                     │
│  ├── Settlement engine (submits Stellar transactions)          │
│  ├── Ledger indexer (syncs on-chain state)                     │
│  ├── Webhook delivery                                           │
│  ├── Reconciliation (5-minute checks)                          │
│  ├── Scheduled payouts                                          │
│  └── OFAC SDN list refresh (daily)                             │
└─────────────────────────────────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Stellar Network                             │
│  - Horizon API (transaction submission, account queries)       │
│  - Testnet: horizon-testnet.stellar.org                        │
│  - Mainnet: horizon.stellar.org                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Key Internal Packages

| Package | Purpose |
|---------|---------|
| `internal/wallet` | Wallet creation, trustlines, balance queries |
| `internal/transfer` | Transfer initiation and status tracking |
| `internal/settlement` | Stellar transaction submission |
| `internal/fx` | FX quotes and conversions via path payments |
| `internal/compliance` | Sanctions screening, velocity checks |
| `internal/webhook` | Event delivery with signature verification |
| `internal/fiat` | Flutterwave/Yellow Card fiat rails |

---

## Quick Start

### Option 1: Docker (Recommended)

The fastest way to run Fluxa locally:

```bash
# Clone the repository
git clone https://github.com/Savitura/Fluxa.git
cd Fluxa

# Generate an encryption key
export MASTER_ENCRYPTION_KEY=$(openssl rand -hex 32)

# Start all services
docker compose up --build -d

# Check health
curl http://localhost:3000/health
```

This starts:
- **API** on `http://localhost:3000`
- **Worker** for background jobs
- **PostgreSQL 15** on port 5432
- **Redis 7** on port 6379
- **Migrate** (one-shot): runs `api -migrate-only` before the API/worker boot, so the database is always on the latest schema — no manual `make migrate` needed.

Images are built from `Dockerfile.api` (`cmd/api`) and `Dockerfile.worker` (`cmd/worker`).

For development with hot reload (rebuilds + restarts on source changes):

```bash
docker compose -f docker-compose.yml -f docker-compose.dev.yml watch
```

The dev override bind-mounts the source tree (read-only, for inspection) and watches `cmd/`, `internal/`, and `go.mod`/`go.sum`.

### Option 2: Local Development

Prerequisites: Go 1.22+, PostgreSQL 15+, Redis 7+

```bash
# Clone and setup
git clone https://github.com/Savitura/Fluxa.git
cd Fluxa
go mod tidy

# Configure environment
cp .env.example .env
# Edit .env:
#   DATABASE_URL=postgresql://user:password@localhost:5432/fluxa?sslmode=disable
#   REDIS_URL=redis://localhost:6379
#   MASTER_ENCRYPTION_KEY=<output of: openssl rand -hex 32>

# Run migrations
make migrate

# Start the API (Terminal 1)
make run-api

# Start the worker (Terminal 2)
make run-worker
```

---

## Demo Walkthrough

Once Fluxa is running locally, try the complete flow:

### 1. Register and get a JWT

```bash
curl -X POST http://localhost:3000/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Demo Fintech",
    "email": "demo@example.com",
    "password": "secure-password-123"
  }'
```

Response:
```json
{
  "user": {
    "id": "0193b0b4-1b33-7e9a-bcf6-...",
    "email": "demo@example.com",
    "name": "Demo Fintech",
    "created_at": "2026-06-22T12:00:00Z"
  },
  "tenant": {
    "id": "0193b0b4-1b33-7e9a-bcf6-...",
    "name": "Demo Fintech",
    "created_at": "2026-06-22T12:00:00Z"
  },
  "role": "owner",
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs..."
}
```

Use the `access_token` as `Authorization: Bearer <access_token>`. (`POST /v1/auth/login` with `{ "email", "password" }` returns the same shape; `POST /v1/auth/refresh` with `{ "refresh_token" }` mints a new pair.)

### 2. Create an API key

```bash
curl -X POST http://localhost:3000/v1/keys \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <jwt_token>" \
  -d '{"label": "Demo Key"}'
```

Save the `key` value — it's shown only once.

### 3. Create a wallet

```bash
curl -X POST http://localhost:3000/v1/wallets \
  -H "Authorization: Bearer sk_live_..."
```

### 4. Fund it on testnet

```bash
curl "https://friendbot.stellar.org?addr=<PUBLIC_KEY>"
```

### 5. Check balances

```bash
curl -H "Authorization: Bearer sk_live_..." \
  "http://localhost:3000/v1/wallets/<wallet_id>/balances"
```

### 6. Transfer between wallets

Create a second wallet, fund it, then transfer:

```bash
curl -X POST http://localhost:3000/v1/transfers \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk_live_..." \
  -H "Idempotency-Key: 123e4567-e89b-42d3-a456-426614174000" \
  -d '{
    "from_wallet_id": "<sender_id>",
    "to_wallet_id": "<recipient_id>",
    "asset": "XLM",
    "amount": "10.0000000"
  }'
```

The transfer returns `202 Accepted` with `status: pending`. The worker submits the Stellar transaction in the background. Poll the transfer endpoint or register a webhook to get notified when it settles.

See [docs/quickstart.md](docs/quickstart.md) for the complete 10-step integration guide including USDC trustlines, FX quotes, and webhooks.

---

## Configuration

Key environment variables (see [.env.example](.env.example) for the full list):

| Variable | Description |
|----------|-------------|
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_URL` | Redis connection string |
| `MASTER_ENCRYPTION_KEY` | 64-char hex string for wallet key encryption |
| `STELLAR_NETWORK` | `testnet` or `pubnet` |
| `STELLAR_HORIZON_URL` | Horizon API endpoint |
| `COMPLIANCE_ENABLED` | Enable OFAC screening (recommended in production) |

---

## TypeScript SDK

```bash
npm install @savitura/fluxa
```

```typescript
import { FluxaClient } from "@savitura/fluxa";

const client = new FluxaClient({ apiKey: "sk_live_..." });

const wallet = await client.wallets.create();
const tx = await client.transfers.create({
  from_wallet_id: wallet.id,
  to_wallet_id: "recipient-id",
  asset: "USDC",
  amount: "100.0000000",
});
```

See [sdk/README.md](sdk/README.md) for full documentation.

---

## Documentation

- [Interactive API Docs (Swagger UI)](http://localhost:3000/docs) — Explore and test endpoints interactively (served at `/docs`)
- [OpenAPI 3.0 Specification](docs/openapi.yaml) — Raw OpenAPI YAML spec (also available at `/docs/openapi.yaml`)
- [Quickstart Guide](docs/quickstart.md) — Complete integration walkthrough
- [Error Reference](docs/errors.md) — API error codes and resolutions
- [Idempotency](docs/idempotency.md) — Safe retries with idempotency keys
- [Webhook Verification](docs/webhook-verification/README.md) — Signature verification in Go/TypeScript
- [Failover](FAILOVER.md) — Multi-region deployment and disaster recovery
- [Contributing](CONTRIBUTING.md) — Development setup and contribution guidelines

---

## License

[MIT](LICENSE)
