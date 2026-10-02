# Webhook dead-letter queue and operator replay

When a webhook delivery exhausts its retry schedule it is retained as a
**dead letter** rather than discarded. Operators can inspect the exhausted
deliveries and replay them safely.

## Lifecycle

1. A delivery is attempted up to `maxAttempts` times (five, with the standard
   backoff schedule).
2. The final failure records the error, moves the delivery to the
   `dead_lettered` state, and writes a row in `webhook_dead_letters` with the
   original event identity, the payload, and the attempt count.
3. The dead letter stays `pending` until an operator replays or discards it.

Dead letters live in Postgres, so they survive a worker restart and remain
queryable.

## API

| Method | Path | Scope |
| --- | --- | --- |
| `GET` | `/v1/webhooks/dead-letters` | `webhooks:read` |
| `GET` | `/v1/webhooks/dead-letters/{id}` | `webhooks:read` |
| `POST` | `/v1/webhooks/dead-letters/{id}/replay` | `webhooks:write`, Owner/Admin |

The list endpoint accepts `event`, `endpoint_id`, `status`, `since`, `until`,
`limit` (max 200), and `offset`. Every response is scoped to the authenticated
tenant; another tenant's dead letter is a `404`, never a `403`, so the ID space
cannot be enumerated.

The detail endpoint includes the append-only attempt history. Each attempt
carries a `kind` of `automatic` (the worker's own retry) or `replay` (an
operator-initiated resend).

## Replay semantics

`POST /v1/webhooks/dead-letters/{id}/replay` requires an `Idempotency-Key`
header. A replay:

- targets the **original endpoint** only, and only while it is active and owns
  the same tenant (otherwise `409`);
- preserves the **event identity** — the original `event_type` and body;
- issues a **new delivery identity** and records it on the dead letter;
- is delivered through the normal path, so it is signed with the endpoint's
  **current signing secret** and the standard timestamped `X-Fluxa-Signature`;
- is **idempotent**: repeated or concurrent requests for the same dead letter
  return the same new delivery instead of resending it. This is enforced by a
  `SELECT ... FOR UPDATE` row lock plus a unique replay token.

## Redaction and retention

- Sensitive payload keys (`secret`, `token`, `password`, `authorization`,
  `api_key`, `private_key`, …, matched case-insensitively and recursively) are
  replaced with `[REDACTED]` on every read. The retained payload is *not*
  modified, so a replay still sends the original body.
- `redacted_fields` lists the JSON paths that were hidden, so a client can
  render "this field is redacted" instead of "this field is missing".
- Dead letters are retained for 30 days. The sweep runs on the list path and
  removes expired rows; a `retain_until` timestamp is recorded at creation.
