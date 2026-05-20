# Fluxa

**Cross-border payment infrastructure for emerging markets.**

Fluxa is a programmable remittance API built on the [Stellar](https://stellar.org) network. It provides the primitives fintech products need to move value across borders — wallet management, internal transfers, FX conversion via Stellar path payments, and ledger indexing — behind a clean REST API.

> **Status**: Early development. Testnet only.

---

## Architecture

```
Client Applications
        │
        ▼
┌─────────────────────┐
│    Fluxa REST API   │  Chi router, JSON over HTTP
└─────────────────────┘
        │
        ▼
┌─────────────────────────────────────────────────────┐
│                   Core Services                     │
│  ┌─────────────┐  ┌──────────────┐  ┌───────────┐  │
│  │   Wallet    │  │   Transfer   │  │    FX     │  │
│  │   Service   │  │   Service    │  │  Service  │  │
│  └─────────────┘  └──────────────┘  └───────────┘  │
│  ┌──────────────────────┐  ┌──────────────────────┐ │
│  │  Settlement Engine   │  │   Ledger Indexer     │ │
│  └──────────────────────┘  └──────────────────────┘ │
└─────────────────────────────────────────────────────┘
        │                          │
        ▼                          ▼
┌──────────────┐          ┌────────────────┐
│  PostgreSQL  │          │  Asynq / Redis │
│  (pgx/v5)   │          │  (job queue)   │
└──────────────┘          └────────────────┘
        │
        ▼
┌──────────────────────┐
│    Stellar Network   │  Horizon API
│    (testnet/mainnet) │
└──────────────────────┘
```

### Two Processes

| Binary         | Role |
|----------------|------|
| `cmd/api`      | HTTP server — handles all REST requests, enqueues async work |
| `cmd/worker`   | Asynq worker — processes transfers, runs ledger indexer |

Transfers are **asynchronous**. `POST /v1/transfers` returns `202 Accepted` with a `pending` transaction immediately. The worker submits it to Stellar and updates the status to `confirmed` or `failed`. Poll `GET /v1/transfers/:id` for status.

---

## Project Structure

```
fluxa/
├── cmd/
│   ├── api/main.go          # API server entry point
│   └── worker/main.go       # Background worker entry point
├── internal/
│   ├── config/              # Viper-based env config
│   ├── domain/              # Core types: Wallet, Transaction, Conversion, errors
│   ├── crypto/              # AES-256-GCM encrypt/decrypt (stdlib only)
│   ├── stellar/             # Horizon client, keypair generation, signer interface
│   ├── postgres/            # pgx/v5 repository implementations
│   ├── queue/               # Asynq client + task type definitions
│   ├── wallet/              # Wallet service + HTTP handler
│   ├── transfer/            # Transfer service + HTTP handler
│   ├── fx/                  # FX/conversion service + HTTP handler
│   ├── settlement/          # Settlement engine + Asynq task handler
│   ├── indexer/             # Ledger indexer + Asynq periodic task
│   ├── server/              # Chi router setup, middleware
│   └── api/                 # Shared request validation + response helpers
└── db/
    └── migrations/          # golang-migrate SQL files
```

---

## Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- PostgreSQL 15+
- Redis 7+
- (Optional) [`migrate` CLI](https://github.com/golang-migrate/migrate/tree/master/cmd/migrate) for manual migrations

---

## Getting Started

### 1. Clone and install dependencies

```bash
git clone https://github.com/yourusername/fluxa
cd fluxa
go mod tidy
```

### 2. Configure environment

```bash
cp .env.example .env
```

Edit `.env`:

| Variable | Description |
|---|---|
| `PORT` | HTTP listen port (default: `3000`) |
| `ENV` | `development` or `production` |
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_URL` | Redis connection string |
| `STELLAR_NETWORK` | `testnet` or `mainnet` |
| `STELLAR_HORIZON_URL` | Horizon endpoint |
| `STELLAR_USDC_ISSUER` | USDC issuer public key |
| `MASTER_ENCRYPTION_KEY` | 64 hex chars (32 bytes) — encrypts stored secret keys |
| `TREASURY_SECRET_KEY` | Stellar secret key that funds new accounts (testnet: leave empty) |

Generate a master key:
```bash
openssl rand -hex 32
```

### 3. Run migrations

```bash
make migrate
```

### 4. Start the API

```bash
make run-api
```

### 5. Start the worker (separate terminal)

```bash
make run-worker
```

---

## API Reference

All endpoints are prefixed `/v1`. Responses are JSON. Errors follow the format:

```json
{
  "error": {
    "code": "WALLET_NOT_FOUND",
    "message": "wallet not found"
  }
}
```

---

### Wallets

#### Create Wallet

```http
POST /v1/wallets
```

Generates a new Stellar keypair, encrypts the secret key with AES-256-GCM, and persists it. The secret key is **never returned**.

**Response** `201 Created`
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "public_key": "GABC...XYZ",
  "created_at": "2026-05-20T10:00:00Z"
}
```

---

#### Get Balances

```http
GET /v1/wallets/:id/balances
```

Fetches live balances directly from Horizon. Returns empty array if the account is not yet funded.

**Response** `200 OK`
```json
{
  "wallet_id": "550e8400-e29b-41d4-a716-446655440000",
  "balances": [
    { "asset_code": "XLM",  "issuer": "",          "balance": "100.0000000" },
    { "asset_code": "USDC", "issuer": "GBBD47...",  "balance": "50.0000000" }
  ]
}
```

---

### Transfers

#### Initiate Transfer

```http
POST /v1/transfers
Content-Type: application/json

{
  "from_wallet_id": "550e8400-e29b-41d4-a716-446655440000",
  "to_wallet_id":   "661f9511-f30c-52e5-b827-557766551111",
  "asset":          "USDC",
  "amount":         "10.50"
}
```

Returns `202 Accepted` immediately. Transfer is processed asynchronously.

**Response** `202 Accepted`
```json
{
  "id":          "772g0622-...",
  "type":        "transfer",
  "status":      "pending",
  "from_wallet": "550e8400-...",
  "to_wallet":   "661f9511-...",
  "asset":       "USDC",
  "amount":      "10.5",
  "created_at":  "2026-05-20T10:01:00Z"
}
```

**Status flow**: `pending` → `submitted` → `confirmed` | `failed`

---

#### Get Transaction

```http
GET /v1/transfers/:id
```

**Response** `200 OK` — same shape as above, with `tx_hash` populated once confirmed.

---

### Transactions

#### List Transactions

```http
GET /v1/transactions?wallet_id=<uuid>&limit=20&offset=0
```

**Response** `200 OK`
```json
{
  "transactions": [ ... ]
}
```

---

### FX / Conversion

#### Get Quote

```http
POST /v1/fx/quote
Content-Type: application/json

{
  "source_asset": "XLM",
  "dest_asset":   "USDC",
  "amount":       "100"
}
```

**Response** `200 OK`
```json
{
  "source_asset":  "XLM",
  "dest_asset":    "USDC",
  "source_amount": "350.123456",
  "dest_amount":   "100",
  "rate":          "0.285621",
  "expires_at":    "2026-05-20T10:01:30Z"
}
```

Quotes expire in **30 seconds**.

---

#### Execute Conversion

```http
POST /v1/fx/convert
Content-Type: application/json

{
  "wallet_id":    "550e8400-...",
  "source_asset": "XLM",
  "dest_asset":   "USDC",
  "amount":       "100"
}
```

Fetches a fresh quote and executes it via Stellar path payments.

**Response** `200 OK`
```json
{
  "id":            "883h1733-...",
  "wallet_id":     "550e8400-...",
  "source_asset":  "XLM",
  "dest_asset":    "USDC",
  "source_amount": "350.123456",
  "dest_amount":   "100",
  "rate":          "0.285621",
  "created_at":    "2026-05-20T10:01:00Z"
}
```

---

### Health Check

```http
GET /health
```

**Response** `200 OK`
```json
{ "status": "ok" }
```

---

## Security

- **Key storage**: Stellar secret keys are encrypted with AES-256-GCM before being stored. The 32-byte master key lives only in the environment — never in the database or logs.
- **No key exposure**: Secret keys are never returned by any API endpoint. The `public_key` field is the only wallet identifier returned.
- **Signer abstraction**: The `stellar.Signer` interface in `internal/stellar/signer.go` isolates all signing logic. Replace `EnvSigner` with an HSM or AWS KMS implementation without touching the settlement engine.
- **Financial arithmetic**: All monetary values use `shopspring/decimal` — no floating-point.

---

## Development

### Run tests

```bash
make test
```

The `internal/crypto` package has unit tests that run with no external dependencies:

```bash
go test ./internal/crypto/... -v
```

### Build binaries

```bash
make build
# outputs: bin/api, bin/worker
```

### Testnet Funding

On testnet, fund a newly created wallet via [Stellar Friendbot](https://friendbot.stellar.org):

```bash
curl "https://friendbot.stellar.org?addr=<PUBLIC_KEY>"
```

---

## Roadmap

- [ ] Webhook events on transaction status changes
- [ ] API key authentication middleware
- [ ] Trustline management endpoints
- [ ] Local currency rails (NGN, KES, GHS, ZAR) via Stellar Anchors
- [ ] Go SDK client package
- [ ] OpenAPI / Swagger spec

---

## License

MIT
