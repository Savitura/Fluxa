ALTER TABLE transactions ADD COLUMN IF NOT EXISTS fiat_rail VARCHAR(50);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS fiat_provider_ref VARCHAR(255);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS fiat_status VARCHAR(50);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS local_currency VARCHAR(10);
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS local_amount DECIMAL(20,4);

CREATE INDEX IF NOT EXISTS idx_transactions_fiat_ref ON transactions(fiat_rail, fiat_provider_ref);

ALTER TABLE fiat_deposits ADD COLUMN IF NOT EXISTS instructions JSONB;

CREATE TABLE IF NOT EXISTS audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID REFERENCES transactions(id),
    fiat_deposit_id UUID REFERENCES fiat_deposits(id),
    fiat_withdrawal_id UUID REFERENCES fiat_withdrawals(id),
    action VARCHAR(50) NOT NULL,
    details JSONB,
    provider VARCHAR(50),
    provider_ref VARCHAR(255),
    local_amount DECIMAL(20,4),
    local_currency VARCHAR(10),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_transaction_id ON audit_log(transaction_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at);
