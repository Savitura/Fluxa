CREATE TABLE webhook_endpoints (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,
    url         TEXT NOT NULL,
    secret      TEXT NOT NULL,
    events      TEXT[] NOT NULL DEFAULT '{}',
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_endpoints_tenant ON webhook_endpoints(tenant_id);

CREATE TYPE webhook_delivery_status AS ENUM ('pending', 'success', 'failed');

CREATE TABLE webhook_deliveries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id    UUID NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    event_type     TEXT NOT NULL,
    payload        JSONB NOT NULL,
    status         webhook_delivery_status NOT NULL DEFAULT 'pending',
    response_code  INT,
    attempt_count  INT NOT NULL DEFAULT 0,
    last_attempt   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_deliveries_endpoint ON webhook_deliveries(endpoint_id);
CREATE INDEX idx_webhook_deliveries_status   ON webhook_deliveries(status);
