-- Operator replay for the webhook dead-letter queue.
--
-- Dead letters already retained the payload and the final error. This migration
-- adds the lifecycle fields an operator needs to inspect and safely replay an
-- exhausted delivery: the original event identity, a replay state machine, the
-- idempotency token that makes concurrent replays collapse to one, and a
-- retention deadline.
--
-- Attempt history lives in its own append-only table so it survives even after
-- a delivery row is pruned, and so automatic retries can be distinguished from
-- operator replays with an explicit `kind`.

ALTER TABLE webhook_dead_letters
    ADD COLUMN IF NOT EXISTS event_type TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS replay_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_replayed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS replay_token TEXT,
    ADD COLUMN IF NOT EXISTS replay_delivery_id UUID,
    ADD COLUMN IF NOT EXISTS redacted_fields TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS retain_until TIMESTAMPTZ;

-- Tenant-scoped filtering by status and event is the list endpoint's hot path.
CREATE INDEX IF NOT EXISTS idx_webhook_dead_letters_status
    ON webhook_dead_letters(tenant_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_dead_letters_event
    ON webhook_dead_letters(tenant_id, event_type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_webhook_dead_letters_retention
    ON webhook_dead_letters(retain_until)
    WHERE retain_until IS NOT NULL;

-- One replay claim per token. Combined with the row lock taken while replaying,
-- this makes a concurrent replay idempotent rather than a duplicate delivery.
CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_dead_letters_replay_token
    ON webhook_dead_letters(replay_token)
    WHERE replay_token IS NOT NULL;

-- A replay delivery points back at the dead letter it replays, which keeps the
-- event identity verbatim while giving the operator a new delivery identity.
ALTER TABLE webhook_deliveries
    ADD COLUMN IF NOT EXISTS replay_of UUID;

CREATE TABLE IF NOT EXISTS webhook_delivery_attempts (
    id UUID PRIMARY KEY,
    delivery_id UUID NOT NULL,
    dead_letter_id UUID,
    tenant_id UUID,
    mode TEXT NOT NULL DEFAULT 'live',
    kind TEXT NOT NULL,
    attempt_number INT NOT NULL,
    status TEXT NOT NULL,
    response_code INT,
    error_message TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_delivery_attempts_delivery
    ON webhook_delivery_attempts(delivery_id, attempt_number);
CREATE INDEX IF NOT EXISTS idx_webhook_delivery_attempts_tenant
    ON webhook_delivery_attempts(tenant_id, occurred_at DESC);
