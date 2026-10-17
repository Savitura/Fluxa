# Fluxa

Cross-border payment infrastructure API.

## Project direction

**Decision (October 2026): keep the general payments API and freeze new features until main is stable.**

Fluxa keeps its current scope — the general payments API — for the next few months. The alternatives considered (narrowing Fluxa into CrowdPay's escrow + payout layer, or pivoting to a non-custodial anchor connector) are rejected for now, and may be revisited after stabilization.

New feature PRs are not merged until both of the following hold:

1. CI is green on `main` (all lint, build, test and format gates passing), and
2. the core ledger and transfer paths have test coverage ([#46](https://github.com/Savitura/Fluxa/issues/46)).

During the freeze:

- **Continues:** bug fixes, test coverage, CI/build stability, documentation, and security fixes.
- **On hold:** new features — for example rate locks, approval policies, data export, test clocks and DLQs, and the open feature issues [#340](https://github.com/Savitura/Fluxa/issues/340), [#327](https://github.com/Savitura/Fluxa/issues/327) and [#4](https://github.com/Savitura/Fluxa/issues/4). These stay open but should be labelled on-hold until the freeze is lifted.

# Fluxa TypeScript SDK

Fluxa is a payment platform built on the Stellar network that provides APIs for managing wallets, transferring assets (including USDC and fiat), handling payment links, refunds, FX conversion, scheduling payouts, and more. It is designed for fintechs and businesses to integrate Stellar payments into their applications.

## Project Layout

- `cmd/api`: HTTP API server (Go) that exposes the Fluxa API endpoints.
- `cmd/worker`: Background worker (Go) that indexes Stellar transactions, processes schedules, handles reconciliation, and performs other asynchronous tasks.
- `apps/web`: Web application (Next.js) for the Fluxa dashboard.
- `sdk/`: TypeScript/JavaScript SDK for interacting with the Fluxa API.
- `sdk-go/`: Go SDK for the Fluxa API.
- `sdk-python/`: Python SDK for the Fluxa API.
- `contracts/soroban/`: Soroban smart contracts (e.g., `contract_wallet` for wallet management on Stellar).

## Running Locally

The easiest way to run Fluxa locally is using Docker Compose. The `docker-compose.yml` file defines the following services:

- `postgres`: PostgreSQL database (port 5432)
- `redis`: Redis cache (port 6379)
- `migrate`: One‑shot database migration that runs before the API and worker start.
- `api`: The API server (port 3000)
- `worker`: The background worker (exposes metrics on port 9090)

To start all services:

```bash
docker compose up
```

The API will be available at `http://localhost:3000`. The worker metrics endpoint is at `http://localhost:9090/metrics`.

### Environment Variables

The services read configuration from environment variables. A `.env.example` file is provided with the required variables. For local development, you can copy it to `.env` and adjust as needed.

Key variables include:

- `STELLAR_NETWORK`: Set to `testnet` for local development (uses Stellar testnet).
- `STELLAR_HORIZON_URL`: Horizon endpoint for the Stellar network (default: `https://horizon-testnet.stellar.org`).
- `STELLAR_USDC_ISSUER`: The issuing account for USDC on Stellar (default: `GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5`, which is the testnet USDC issuer).
- `MASTER_ENCRYPTION_KEY`: Used to encrypt sensitive data stored in the database.
- `PLATFORM_FEE_WALLET_PUBLIC_KEY`: The wallet that collects platform fees.
- `ADMIN_API_KEY`: API key for administrative endpoints.
- `FLUTTERWAVE_SECRET_KEY`: Key for fiat deposits/withdrawals via Flutterwave (set to `mock` for local dev).

### API Keys

Fluxa uses API keys of the form `sk_live_...` for mainnet and `sk_test_...` for testnet. The key prefix determines the environment against which the key authenticates. For local development, use testnet keys (starting with `sk_test_`). You can generate an API key via the API or the SDK.

## SDK Documentation

The TypeScript SDK documentation, including cursor pagination examples, has been moved to `sdk/README.md`.

## Quickstart

For a step‑by‑step guide to integrating Fluxa, see the [quickstart guide](docs/quickstart.md).