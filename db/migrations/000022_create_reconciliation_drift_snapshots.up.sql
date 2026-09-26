CREATE TABLE reconciliation_drift_snapshots (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID REFERENCES tenants(id) ON DELETE SET NULL,
    wallet_id          UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    wallet_address     TEXT NOT NULL,
    asset              TEXT NOT NULL,
    expected_balance   NUMERIC(20, 7) NOT NULL,
    actual_balance     NUMERIC(20, 7) NOT NULL,
    drift_amount       NUMERIC(20, 7) NOT NULL,
    threshold          NUMERIC(20, 7) NOT NULL,
    detected_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_reconciliation_drift_wallet_asset ON reconciliation_drift_snapshots(wallet_id, asset, detected_at DESC);
CREATE INDEX idx_reconciliation_drift_tenant ON reconciliation_drift_snapshots(tenant_id, detected_at DESC);
CREATE INDEX idx_reconciliation_drift_detected_at ON reconciliation_drift_snapshots(detected_at DESC);
