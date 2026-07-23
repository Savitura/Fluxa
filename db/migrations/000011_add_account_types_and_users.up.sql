ALTER TABLE tenants ADD COLUMN account_type VARCHAR(32) NOT NULL DEFAULT 'individual';
ALTER TABLE tenants ADD COLUMN max_wallets INT;
ALTER TABLE tenants ADD COLUMN max_transfers_per_month INT;
ALTER TABLE tenants ADD COLUMN max_webhooks INT;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE organization_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(32) NOT NULL,
    invited_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_tenant_user UNIQUE (tenant_id, user_id)
);

CREATE INDEX idx_org_members_tenant ON organization_members(tenant_id);
CREATE INDEX idx_org_members_user ON organization_members(user_id);

CREATE TABLE organization_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    role VARCHAR(32) NOT NULL,
    token TEXT NOT NULL UNIQUE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    invited_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_org_invites_tenant ON organization_invites(tenant_id);
CREATE INDEX idx_org_invites_token ON organization_invites(token);
