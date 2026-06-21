ALTER TYPE transaction_status ADD VALUE IF NOT EXISTS 'reconciliation_failed';

ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS reconciled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS requeue_count INT NOT NULL DEFAULT 0;

CREATE TYPE audit_outcome AS ENUM ('ok', 'mismatch', 'not_found');

CREATE TABLE ledger_audit_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tx_id           UUID NOT NULL REFERENCES transactions(id),
    stellar_hash    TEXT NOT NULL,
    checked_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    horizon_status  TEXT NOT NULL DEFAULT '',
    amount_verified BOOLEAN NOT NULL DEFAULT FALSE,
    asset_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    outcome         audit_outcome NOT NULL,
    details         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_audit_log_tx_id ON ledger_audit_log(tx_id);
CREATE INDEX idx_audit_log_checked_at ON ledger_audit_log(checked_at);
