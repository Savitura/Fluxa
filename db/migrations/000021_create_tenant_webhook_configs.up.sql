CREATE TABLE tenant_webhook_configs (
    tenant_id          UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    enabled            BOOLEAN NOT NULL DEFAULT FALSE,
    url                TEXT NOT NULL DEFAULT '',
    secret             TEXT NOT NULL,
    signing_algorithm  TEXT NOT NULL DEFAULT 'hmac-sha256',
    events             TEXT[] NOT NULL DEFAULT '{}',
    paused             BOOLEAN NOT NULL DEFAULT FALSE,
    resume_at          TIMESTAMPTZ,
    last_delivered_at  TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tenant_webhook_deliveries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    event_type     TEXT NOT NULL,
    payload        JSONB NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'success', 'failed', 'paused')),
    response_code  INT,
    attempt_count  INT NOT NULL DEFAULT 0,
    last_attempt   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tenant_webhook_configs_enabled ON tenant_webhook_configs(enabled);
CREATE INDEX idx_tenant_webhook_deliveries_tenant_created ON tenant_webhook_deliveries(tenant_id, created_at DESC);
CREATE INDEX idx_tenant_webhook_deliveries_status ON tenant_webhook_deliveries(status);
