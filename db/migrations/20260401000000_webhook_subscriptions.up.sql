CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36),
    event_type VARCHAR(100) NOT NULL,
    webhook_url TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_subscriptions_tenant_event ON webhook_subscriptions(tenant_id, event_type);
