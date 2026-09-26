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
const client = new FluxaClient({
  apiKey: "sk_live_...",         // Required
  baseUrl: "https://api.fluxa.io", // Default
  timeout: 30000,              // 30s default
  maxRetries: 3,               // Exponential backoff for 5xx/network errors
  retryDelay: 500,             // Base delay in ms
});
```

## Resources

### Wallets

```ts
// Create
const wallet = await client.wallets.create();

// Get balances
const { balances } = await client.wallets.getBalances("wallet-id");
```

### Transfers

```ts
// Single transfer
const tx = await client.transfers.create({
  from_wallet_id: "from",
  to_wallet_id: "to",
  asset: "USDC",
  amount: "100.0000000",
});

// Get by ID
const found = await client.transfers.get("tx-id");

// List transactions for a wallet
const { transactions } = await client.transfers.list({
  wallet_id: "wallet-id",
  limit: 25,
  offset: 0,
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

### FX (Currency Conversion)

```ts
// Get a quote (valid for 30 seconds)
const quote = await client.fx.quote({
  from_asset: "USD",
  to_asset: "USDC",
  amount: "1000",
});

// Execute conversion using a quote
const conversion = await client.fx.convert({
  wallet_id: "wallet-id",
  quote_id: quote.id,
});

// Get current rates
const rates = await client.fx.getRates({ from: "USD", to: "USDC" });
```

### Schedules (Recurring Transfers)

```ts
// Create a weekly schedule
const schedule = await client.schedules.create({
  from_wallet_id: "from",
  to_wallet_id: "to",
  asset: "USDC",
  amount: "50.0000000",
  frequency: "weekly",
  start_date: "2026-09-01T00:00:00Z",
});

// List all schedules
const { schedules } = await client.schedules.list();

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
const newKey = await client.keys.create({ label: "Production" });
console.log(newKey.key); // Shown only once

// List
const keys = await client.keys.list();

// Revoke
await client.keys.delete("key-id");
```

### Fiat (Deposits & Withdrawals)

```ts
// Deposit
const deposit = await client.fiat.deposit("wallet-id", {
  amount: "50000",
  currency: "NGN",
  email: "user@example.com",
  name: "John Doe",
});
// Redirect user to deposit.payment_link

// Withdraw
const withdrawal = await client.fiat.withdraw("wallet-id", {
  amount: "10000",
  currency: "NGN",
  account_bank: "044",
  account_number: "1234567890",
});
```

## Error Handling

```ts
import {
  FluxaError,
  AuthenticationError,
  NotFoundError,
  ValidationError,
  RateLimitError,
} from "@savitura/fluxa";

try {
  await client.transfers.get("nonexistent");
} catch (err) {
  if (err instanceof NotFoundError) {
    console.log("Transfer not found:", err.message);
  } else if (err instanceof RateLimitError) {
    console.log("Rate limited, retry after:", err.retryAfter);
  } else if (err instanceof AuthenticationError) {
    console.log("Bad API key");
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
