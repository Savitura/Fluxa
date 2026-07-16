CREATE TYPE batch_status AS ENUM ('pending', 'processing', 'partial', 'completed', 'failed');

CREATE TABLE batches (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID REFERENCES tenants(id) ON DELETE CASCADE,
    status      batch_status NOT NULL DEFAULT 'pending',
    total_count INT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_batches_tenant_id ON batches(tenant_id);

ALTER TABLE transactions ADD COLUMN batch_id UUID REFERENCES batches(id) ON DELETE SET NULL;
ALTER TABLE transactions ADD COLUMN reference TEXT;

CREATE INDEX idx_transactions_batch_id ON transactions(batch_id);

CREATE TYPE schedule_frequency AS ENUM ('daily', 'weekly', 'monthly');
CREATE TYPE schedule_status AS ENUM ('active', 'paused', 'cancelled', 'completed');

CREATE TABLE schedules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID REFERENCES tenants(id) ON DELETE CASCADE,
    from_wallet UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    to_wallet   UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    asset       TEXT NOT NULL,
    amount      NUMERIC(20, 7) NOT NULL,
    frequency   schedule_frequency NOT NULL,
    next_run_at TIMESTAMPTZ NOT NULL,
    end_at      TIMESTAMPTZ,
    status      schedule_status NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_schedules_tenant_id ON schedules(tenant_id);
CREATE INDEX idx_schedules_due ON schedules(status, next_run_at);
