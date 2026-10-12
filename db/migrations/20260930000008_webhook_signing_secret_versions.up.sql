CREATE TABLE tenant_webhook_signing_secrets (
    key_id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    encrypted_secret   TEXT NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    activated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at         TIMESTAMPTZ,
    status             TEXT NOT NULL CHECK (status IN ('active', 'overlapping', 'retired'))
);

CREATE UNIQUE INDEX idx_tenant_webhook_signing_secrets_active
    ON tenant_webhook_signing_secrets(tenant_id) WHERE status = 'active';
CREATE INDEX idx_tenant_webhook_signing_secrets_tenant_created
    ON tenant_webhook_signing_secrets(tenant_id, created_at DESC);

ALTER TABLE tenant_webhook_configs
    ADD COLUMN signing_key_id UUID REFERENCES tenant_webhook_signing_secrets(key_id);

ALTER TABLE tenant_webhook_deliveries
    ADD COLUMN signing_key_id UUID REFERENCES tenant_webhook_signing_secrets(key_id);
