CREATE TABLE balance_discrepancies (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id        UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    db_balance       NUMERIC(20, 7) NOT NULL,
    horizon_balance  NUMERIC(20, 7) NOT NULL,
    asset            TEXT NOT NULL,
    detected_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at      TIMESTAMPTZ
);

CREATE INDEX idx_balance_discrepancies_wallet_id  ON balance_discrepancies(wallet_id);
CREATE INDEX idx_balance_discrepancies_detected_at ON balance_discrepancies(detected_at DESC);
