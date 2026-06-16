CREATE TABLE fees (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID,
    transfer_fee_bps   INT NOT NULL,
    conversion_fee_bps INT NOT NULL,
    min_fee_amount     NUMERIC(20, 7) NOT NULL DEFAULT 0,
    max_fee_amount     NUMERIC(20, 7),
    asset              TEXT NOT NULL DEFAULT '*',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_fees_tenant_asset ON fees (tenant_id, asset) WHERE tenant_id IS NOT NULL;
CREATE UNIQUE INDEX idx_fees_platform_asset ON fees (asset) WHERE tenant_id IS NULL;

INSERT INTO fees (transfer_fee_bps, conversion_fee_bps, min_fee_amount, max_fee_amount, asset)
VALUES (30, 50, 0, NULL, '*');

CREATE TABLE fee_collections (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    tenant_id      UUID,
    fee_amount     NUMERIC(20, 7) NOT NULL,
    asset          TEXT NOT NULL,
    fee_bps        INT NOT NULL,
    collected_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fee_collections_transaction ON fee_collections(transaction_id);
CREATE INDEX idx_fee_collections_tenant ON fee_collections(tenant_id);
CREATE INDEX idx_fee_collections_collected_at ON fee_collections(collected_at DESC);

ALTER TABLE transactions ADD COLUMN tenant_id UUID;
ALTER TABLE transactions ADD COLUMN fee_bps INT;

ALTER TABLE conversions ADD COLUMN fee_amount NUMERIC(20, 7) NOT NULL DEFAULT 0;
ALTER TABLE conversions ADD COLUMN fee_bps INT;
