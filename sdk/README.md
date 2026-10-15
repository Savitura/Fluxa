# @savitura/fluxa

TypeScript SDK for the [Fluxa](https://fluxa.io) payment API. Zero runtime dependencies — uses native `fetch`.

## Install

```bash
npm install @savitura/fluxa
```

Requires Node.js >= 18.

## Quick Start

```ts
import { FluxaClient } from "@savitura/fluxa";

const client = new FluxaClient({ apiKey: "sk_live_..." });

// Create a wallet
const wallet = await client.wallets.create();
console.log(wallet.id, wallet.public_key);

// Get balances
const { balances } = await client.wallets.getBalances(wallet.id);

// Send a transfer
const tx = await client.transfers.create({
  from_wallet_id: wallet.id,
  to_wallet_id: "recipient-wallet-id",
  asset: "USDC",
  amount: "10.0000000",
});
```

## Configuration

```ts
new FluxaClient({
  apiKey: "sk_live_...",         // Required
  baseUrl: "https://api.fluxa.io", // Default
  timeout: 30000,              // 30s default
  maxRetries: 0,               // Retries are opt-in
  retryDelay: 500,             // Base delay in ms
});
```

## Retries and Idempotency

Fluxa requires an `Idempotency-Key` on financial mutations (wallet creation, transfers,
batches, FX conversions, fiat deposits/withdrawals, schedules, payment links,
refunds, claimable balances, and trustlines). The SDK enforces this as follows:

- Every mutating method generates one UUIT v4 key per call and reuses it across every retry
  attempt, so a timeout or 5xx followed by success produces a single logical operation.
- Pass `idempotencyKey` in the method options to supply your own key. This is required when you
  retry across processes or restarts (e.g. a queue worker that crashed after sending the
  request): the generated key lives only in memory and cannot be recovered after a crash.
- Mutations that the server does not support idlempotency for do not retry by default. They fail
  fast instead of silently repeating a financial operation. You can opt into the old behavior with
  `allowUnsafeRetry: true` on the HTTP layer only if you know the operation is safe to repeat.
- Reads of any kind (GET/HEAD) are always retryable.

```ts
// Generated once, reused across retries automatically.
await client.transfers.create({
  from_wallet_id: "from",
  to_wallet_id: "to",
  asset: "USDC",
  amount: "100.0000000",
});

// Caller-owned key for cross-process retries.
await client.transfers.create(
  {
    from_wallet_id: "from",
    to_wallet_id: "to",
    asset: "USDC",
    amount: "100.0000000",
  },
  { idempotencyKey: "payout-2026-09-28-001" },
);
```

## Resources

### Wallets

```ts
// Create
const wallet = await client.wallets.create();

// Get balances
const { balances } = await client.wallets.getBalances("wallet-id");

// Add a trustline
await client.wallets.createTrustline("wallet-id", {
  asset_code: "USDC",
  asset_issuer: "stellar-issuer-public-key",
});
```

### Transfers

```ts
// Single transfer
const tx = await client.transfers.create({
  from_wallet_id: "from",
  to_wallet_id: "to",
  asset: "USDC",
  amount: "100.0000000",
}, { idempotencyKey: "payout-2026-09-28" });

// Get by ID
const found = await client.transfers.get("tx-ed");

// List transactions for a wallet (backward-compatible raw response)
const { transactions } = await client.transfers.list({
  wallet_id: "wallet-id",
  limit: 25,
  offset: 0,
});

// Single page with standardized Page<T> container
const page = await client.transfers.listPage({
  wallet_id: "wallet-id",
  limit: 25,
});
console.log(page.items, page.nextCursor, page.hasNextPage);

// Stream items across all pages using async iteration
for await (const transfer of client.transfers.iterate({
  wallet_id: "wallet-id",
  limit: 50,
})) {
  console.log(transfer.id, transfer.amount);
  if (transfer.status === "completed") break; // Breaking halts further HTTP requests
}

// Fetch all pages into a consolidated array
const allTransfers = await client.transfers.listAll({
  wallet_id: "wallet-id",
});

// Batch transfers
const batch = await client.transfers.createBatch({
  from_wallet_id: "from",
  transfers: [
    { to_wallet_id: "to1", asset: "USDC", amount: "10" },
    { to_wallet_id: "to2", asset: "USDC", amount: "20", reference: "invoice-42" },
  ],
});

// Get batch status
const status = await client.transfers.getBatch(batch.id);

// Export batch as CSV
const csv = await client.transfers.exportBatch(batch.id);
```

### Payment Links and Refunds

```ts
for (const id of ["invoice-1", "invoice-2"]) {
  const link = await client.paymentLinks.create(
    {
      wallet_id: "wallet-id",
      amount: "2500.00",
      currency: "NGN",
      expires_at: "2026-10-07T12:00:00Z",
    },
    { idempotencyKey: `payment-link:${id}` },
  );
  await client.paymentLinks.cancel(link.id);
}

const refund = await client.refunds.create(
  {
    original_transaction_id: "transaction-id",
    amount: "20.0000000",
    reason: "Order returned",
  },
  { idempotencyKey: "refund:order-42" },
);
const current = await client.refunds.get(refund.id);
```

### FX (Currency Conversion)

```ts
// Get a quote (valid for 30 seconds)
const quote = await client.fx.quote({
  from_asset: "USD",
  to_asset: "USDC",
  amount: "1000",
});

// Execute conversion using a quote
const conversion = await client.fx.convert(
  {
    wallet_id: "wallet-id",
    quote_id: quote.id,
  },
  { idempotencyKey: `fx-convert:${quote.id}` },
);

// Get current rates
const rates = await client.fx.getRates({ from: "USD", to: "USDC" });
```

### Scheduled Tenant Payouts

```ts
// Create a weekly schedule
const schedule = await client.schedules.create({
  from_wallet_id: "from",
  to_wallet_id: "to",
  asset: "USDC",
  amount: "50.0000000",
  frequency: "weekly",
  start_date: "2026-09-01T00:00:00Z",
  timezone: "Africa/Lagos",
  missed_run_policy: "skip",
}, {
  // Reuse this key if you retry the same create request.
  idempotencyKey: "d7f2bc4a-4d87-4f52-82be-356cab14203b",
});

// List all schedules
const { schedules } = await client.schedules.list();
const { runs } = await client.schedules.listRuns(schedule.id, { limit: 20 });

// Pause/resume
await client.schedules.update(schedule.id, { status: "paused" });
await client.schedules.update(schedule.id, { status: "active" });

// Cancel
await client.schedules.delete(schedule.id);
```

### Webhooks

```ts
// Register
const endpoint = await client.webhooks.create({
  url: "https://your-app.com/webhooks/fluxa",
  events: ["transfer.settled", "wallet.funded"],
});
console.log(endpoint.secret); // Store for signature verification

// List
const { endpoints } = await client.webhooks.list();

// Delete
await client.webhooks.delete(endpoint.id);

// List deliveries
const { deliveries } = await client.webhooks.getDeliveries(endpoint.id, {
  limit: 50,
});
```

### Fees

```ts
// Get fee schedule
const fees = await client.fees.get();
// fees.transfer_fee_bps, fees.conversion_fee_bps, fees.min_fee_amount

// Admin: collected fees summary
const { summary } = await client.fees.listCollected({
  start_date: "2026-01-01T00:00:00Z",
  end_date: "2026-12-31T23:59:59Z",
});
```

### API Keys

```ts
// Create
const newKey = await client.keys.create({ title: "Production" });
console.log(newKey.key); // Shown only once

// List (includes each key's scopes, never the secret)
const keys = await client.keys.list();

// Revoke
await client.keys.delete("key-id");
```

### Fiat (Deposits & Withdrawals)

```ts
// Deposit
const deposit = await client.fiat.deposit(
  "wallet-id",
  {
    amount: "50000",
    currency: "NGN",
    email: "user@example.com",
    name: "John Doe",
  },
  { idempotencyKey: `deposit:${Date.now()}` },
);
// Redirect user to deposit.payment_link
// Withdraw
const withdrawal = await client.fiat.withdraw(
  "wallet-id",
  {
    amount: "10000",
    currency: "NGN",
    account_bank: "044",
    account_number: "1234567890",
  },
  { idempotencyKey: `withdrawal:${Date.now()}` },
);
```

## Cursor Pagination

The SDK provides helpers for cursor-based pagination to simplify fetching large datasets.

### Single Page

```typescript
import { FluxaClient } from '@savitura/fluxa';

const client = new FluxaClient({ /* config */ });
const { nodes, pageInfo } = await client.transfers.list(
  { from: 'GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5' },
  { orderBy: 'timestamp', orderDirection: 'desc' },
  { first: 50 }
);

console.log(nodes); // Array of transfers
console.log(pageInfo.hasNextPage); // boolean
console.log(pageInfo.endCursor); // string | null
```

### Streaming with Async Iteration

```typescript
const client = new FluxaClient({ /* config */ });

for await (const transfer of client.transfers.iterate(
  { from: 'GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5' },
  { orderBy: 'timestamp' }
)) {
  console.log(transfer);
  // Break early to stop further requests
  if (someCondition) break;
}
```

### Fetch All Pages

```typescript
const allTransfers = await client.transfers.listAll(
  { from: 'GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5' },
  { orderBy: 'timestamp' }
);
```

### Aborting Requests

```typescript
const controller = new AbortController();

setTimeout(() => controller.abort(), 5000); // Abort after 5 seconds

try {
  for await (const transfer of client.transfers.iterate(
    { from: 'GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5' },
    undefined,
    undefined,
    controller.signal
  )) {
    console.log(transfer);
  }
} catch (error) {
  if (error.name === 'CursorPaginationError') {
    console.log('Iteration was aborted');
  }
}
## Error Handling

```ts
import {
  FluxaError,
  AuthenticationError,
  NotFoundError,
  ValidationError,
  RateLimitError,
  RepeatedCursorError,
} from "@savitura/fluxa";

try {
  await client.transfers.get("nonexistent");
} catch (err) {
  if (err instanceof NotFoundError) {
    console.log("Transfer not found:", err.message);
  } else if (err instanceof RateLimitError) {
    console.log("Rate limited, retry after:", err.retryAfter);
  } else if (err instanceof RepeatedCursorError) {
    console.log("Malformed cursor loop detected:", err.cursor);
  } else if (err instanceof AuthenticationError) {
    console.log("Bad API key");
  } else if (err instanceof PermissionError) {
    // 403 INSUFFICIENT_SCOPE: the key is valid but lacks a scope
    console.log("API key is missing scope:", err.requiredScope);
  } else if (err instanceof FluxaError) {
    console.log(`API error ${err.statusCode}: [${err.code}] ${err.message}`);
  }
}
```

## Abort / Timeouts

Every method accepts an `AbortSignal` for cancellation:

```ts
const controller = new AbortController();
setTimeout(() => controller.abort(), 5000);

const wallet = await client.wallets.create({ signal: controller.signal });
```

## License

MIT
