CREATE TABLE payment_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    mode VARCHAR(20) NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test')),
    amount DECIMAL(20,4) NOT NULL CHECK (amount > 0),
    currency VARCHAR(10) NOT NULL,
    idempotency_key VARCHAR(255),
    status VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'processing', 'paid', 'failed', 'cancelled')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payment_links_tenant_created ON payment_links(tenant_id, created_at DESC);
CREATE UNIQUE INDEX uq_payment_links_idempotency
    ON payment_links(tenant_id, mode, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX idx_payment_links_expiry ON payment_links(expires_at) WHERE status = 'active';

ALTER TABLE fiat_deposits
    ADD COLUMN payment_link_id UUID REFERENCES payment_links(id) ON DELETE SET NULL,
    ADD COLUMN tenant_id UUID REFERENCES tenants(id) ON DELETE CASCADE,
    ADD COLUMN mode VARCHAR(20) NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test'));

CREATE TABLE refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mode VARCHAR(20) NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test')),
    original_transaction_id UUID NOT NULL REFERENCES transactions(id),
    refund_transaction_id UUID UNIQUE REFERENCES transactions(id),
    amount DECIMAL(20,7) NOT NULL CHECK (amount > 0),
    reason TEXT NOT NULL DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'requested'
        CHECK (status IN ('requested', 'pending', 'succeeded', 'failed')),
    idempotency_key VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refunds_tenant_created ON refunds(tenant_id, created_at DESC);
CREATE INDEX idx_refunds_original ON refunds(original_transaction_id);
CREATE UNIQUE INDEX uq_refunds_idempotency
    ON refunds(tenant_id, mode, idempotency_key) WHERE idempotency_key IS NOT NULL;