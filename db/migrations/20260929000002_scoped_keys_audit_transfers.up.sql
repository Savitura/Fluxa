-- Add scopes to API keys
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS scopes TEXT[] NOT NULL DEFAULT '{}';

-- Append-only tenant audit events table
CREATE TABLE IF NOT EXISTS tenant_audit_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mode TEXT NOT NULL DEFAULT 'live',
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    ip_address TEXT,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tenant_audit_events_tenant_created ON tenant_audit_events(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tenant_audit_events_tenant_action ON tenant_audit_events(tenant_id, action);
CREATE INDEX IF NOT EXISTS idx_tenant_audit_events_tenant_actor ON tenant_audit_events(tenant_id, actor_id);
CREATE INDEX IF NOT EXISTS idx_tenant_audit_events_tenant_mode ON tenant_audit_events(tenant_id, mode);

-- Add external_reference and tags to transactions
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS external_reference VARCHAR(100);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_transactions_tenant_ext_ref ON transactions(tenant_id, external_reference) WHERE external_reference IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_transactions_tags ON transactions USING GIN(tags);
