# Preflight network-fee estimation

`POST /v1/fees/estimate` returns what a transfer or batch will cost *before* any
funds move. It is a read-only preflight: it does not create a transaction, and
it is scoped to the authenticated tenant's fee schedule.

## Request

```json
{ "type": "batch", "asset": "USDC", "amount": "2500.00", "destinations": 250 }
```

- `type` is `transfer` or `batch`.
- `amount` is the **total gross amount** across all destinations.
- `destinations` is required for `batch`; each destination is one payment
  operation.

## Response

```json
{
  "type": "batch",
  "asset": "USDC",
  "gross_amount": "2500.0000000",
  "platform_fee": "6.2500000",
  "platform_fee_bps": 25,
  "net_amount": "2493.7500000",
  "network_fee": "0.0025000",
  "network_fee_stroops": 25000,
  "total_fee": "6.2525000",
  "base_fee_stroops": 100,
  "operation_count": 250,
  "transaction_count": 3,
  "max_operations_per_transaction": 100,
  "expires_at": "2026-10-02T00:00:00Z"
}
```

- **`platform_fee`** comes from the tenant's schedule (including volume tiers),
  using the same code path as an actual transfer.
- **`network_fee`** is always denominated in **XLM**, because Stellar fees are
  paid in the native asset regardless of the asset being moved. It is
  `base_fee_stroops × operation_count`.
- **`transaction_count`** is how many Stellar transactions the operations are
  packed into, at most `max_operations_per_transaction` (the protocol cap of
  100) per transaction.
- **`expires_at`** is a one-minute freshness bound: network fees move with
  ledger congestion, so an older estimate should be re-requested.

## Base-fee source

The estimate reads the current base fee from Horizon's `/fee_stats`, using the
recent **maximum** base fee so surge pricing is reflected, with the protocol
minimum (100 stroops) as a floor.

- Configuration: `STELLAR_HORIZON_URL` (already used elsewhere).
- If Horizon is unreachable or returns malformed data, the source returns the
  configured fallback (the protocol minimum) — **an estimate never fails because
  the network is slow**. Failure to reach Horizon is not surfaced as a 500.
- Deployments can substitute a fixed source via `fees.NewEstimatorService`.

## Limits

- `destinations` is capped at 10,000 operations per request.
- `amount` must be a positive decimal.
- A missing estimator wiring yields `404` rather than a panic, matching the
  feature-detection pattern used for optional webhook services.
