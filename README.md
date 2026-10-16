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

## Cursor Pagination

The SDK provides helpers for cursor-based pagination to simplify fetching large datasets.

### Single Page

```typescript
import { FluxaClient } from '@savitura/fluxa';

const client = new FluxaClient({ /* config */ });
const { nodes, pageInfo } = await client.transfers.list(
  { from: '0x123...' },
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
  { from: '0x123...' },
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
  { from: '0x123...' },
  { orderBy: 'timestamp' }
);
```

### Aborting Requests

```typescript
const controller = new AbortController();

setTimeout(() => controller.abort(), 5000); // Abort after 5 seconds

try {
  for await (const transfer of client.transfers.iterate(
    { from: '0x123...' },
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
```
