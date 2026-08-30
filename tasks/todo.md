# Issue #69 — Transaction Screening, OFAC Sanctions & Suspicious Activity Flagging

Branch: `feat/issue-69-compliance-screening`

## Phase 0 — Unblock: repair broken `main` (pre-existing, not part of #69)

`main` did not compile. Root cause: bad merge resolution in `8c6e601`
("Merge remote-tracking branch 'upstream/main' into fix/issue-85-86…") plus
follow-on mock drift. Fixed on this branch so the plan's verification
(`go build`, `make test`, `make lint`) can actually run.

- [x] `internal/domain/wallet.go` — `SyncCursor` declared twice
- [x] `internal/domain/transaction.go` — restore 5 fiat fields the repo layer reads/writes
- [x] `internal/stellar/client.go` — drop dead `PaymentsForAccount` (removed Horizon API)
- [x] `internal/fiat/service.go` — `evt.Reference` → `evt.ProviderRef`
- [x] `internal/fiat/flutterwave/provider.go` — missing `decimal` import
- [x] `internal/postgres/transaction_repo.go` — unused `localAmt`
- [x] `internal/anchor/repository.go` — tenant-scoped signatures to match impl
- [x] `cmd/api/main.go` — restore Flutterwave rail via `NewRailAdapter` (user decision)
- [x] Test mock drift: transfer ×2, batch, indexer, schedule, org
- [x] `go build ./...`, `go vet ./...`, `go test ./... -race` all green (19 pkgs)

## Phase 1 — Schema

- [x] `000022_add_compliance_hold_status.{up,down}.sql` (one-line ALTER TYPE, alone)
- [x] `000023_create_compliance_tables.{up,down}.sql` (4 tables + hot-path indexes)

## Phase 2 — Domain

- [x] `domain.StatusComplianceHold`
- [x] `domain/compliance.go` — review/block/sanctions types + screening request/decision
- [x] 3 sentinel errors + `HandleDomainError` arms (403 `TRANSFER_BLOCKED_SANCTIONS`)
- [x] 4 webhook event types

## Phase 3 — `internal/compliance/`

- [x] `screener.go` — interface, composite, precedence blocked > hold > clear
- [x] `levenshtein.go` — distance-capped edit distance
- [x] `sanctions.go` — `SanctionsSet` + `SanctionsScreener`
- [x] `sdn.go` — `SDNSource` + streaming XML parser
- [x] `velocity.go` — velocity / structuring / round-trip
- [x] `repository.go`, `service.go`, `handler.go`, `worker.go`
- [x] `testdata/sdn_sample.xml`

## Phase 4 — Integration

- [x] `internal/postgres/compliance_repo.go` (tenant-scoped)
- [x] `transfer/service.go` — screen in `initiate()`, `WithScreener` builder
- [x] `queue` task type + enqueue helper
- [x] `server.go` mount at `/admin/compliance`
- [x] `cmd/api` + `cmd/worker` wiring
- [x] config + `.env.example`
- [x] batch `aggregateStatus` — explicit `compliance_hold` arm

## Phase 5 — Tests (acceptance criteria)

- [x] Sanctioned address → 403, zero enqueues
- [x] 3×999 holds, 3×1000 does not
- [x] SDN refresh parses + records update row
- [x] Approve resets to `pending` AND enqueues
- [x] Hold does not block the org's other transfers
- [x] Fuzzy federation match → hold, not blocked
- [x] Composite precedence, fail-closed, refresh_failed webhook
- [x] `compliance_hold` invisible to reconciliation

## Phase 6 — Docs

- [x] `docs/errors.md`, `docs/openapi.yaml`, `README.md`

---

## Review

**Delivered.** Screening runs in `transfer.service.initiate()`, covering the
API, batch and scheduled-payout paths from one call site.

Verification (all run, all green):

```
go build ./...                       clean
go vet ./...                         clean
go test ./... -race -count=1         20 packages, 0 failures
apps/web: npm run lint / build       clean (1 pre-existing warning)
sdk: npm run typecheck / build       clean
```

58 tests in `internal/compliance`, 13 more covering screening in
`internal/transfer`.

### Deviations from plan.md

- **Phase 0 was not in the plan.** `main` did not compile. Repaired 8 build
  errors plus test-mock drift across 5 packages before any of this could be
  verified, plus four more found only by running against a real Postgres
  (nullable fee scan, `COUNT(*) ... FOR UPDATE`, a 16-vs-21 column scan in
  `ListByBatch`, and both enum-rollback migrations).
- **`docs/fluxa.postman_collection.json` and `apps/web/src/lib/api.ts`** were
  updated too — CLAUDE.md's cross-cutting rule requires it and the plan's file
  list omitted them. No dashboard pages were added; the issue didn't ask.
- **Reconciler guard added.** The plan wanted a test asserting held rows are
  invisible to reconciliation. That invariant lived only in SQL, which the
  tests cannot reach, so `RecoverPending` now skips `compliance_hold`
  explicitly — making it testable and refactor-proof.
- **`held_count` added to the batch response** alongside the new
  `compliance_hold` aggregate status, so a partially-held batch is legible.

### Known gap

`ScreeningRequest.ToFederation` is always empty: `domain.Wallet` has no
counterparty-name field, so the fuzzy name rule cannot fire in production. It
is implemented and unit-tested and starts working as soon as a name reaches
the screener. Closing it needs a wallet-name migration the issue didn't ask
for.
