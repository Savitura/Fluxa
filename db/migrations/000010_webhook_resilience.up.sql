ALTER TABLE webhook_endpoints
    ADD COLUMN IF NOT EXISTS success_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failure_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_delivered_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS notified_failing BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE webhook_deliveries
    ADD COLUMN IF NOT EXISTS tenant_id UUID,
    ADD COLUMN IF NOT EXISTS response_body TEXT,
    ADD COLUMN IF NOT EXISTS max_attempts INT NOT NULL DEFAULT 5,
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_attempt TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS webhook_dead_letters (
    id UUID PRIMARY KEY,
    endpoint_id UUID NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    tenant_id UUID,
    delivery_id UUID NOT NULL,
    payload TEXT NOT NULL,
    error_message TEXT NOT NULL,
    attempt_count INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_dead_letters_tenant ON webhook_dead_letters(tenant_id);
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_next_attempt ON webhook_deliveries(next_attempt_at) WHERE status = 'pending';
