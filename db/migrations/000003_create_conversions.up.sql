CREATE TABLE conversions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id     UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    source_asset  TEXT NOT NULL,
    dest_asset    TEXT NOT NULL,
    source_amount NUMERIC(20, 7) NOT NULL,
    dest_amount   NUMERIC(20, 7) NOT NULL,
    rate          NUMERIC(20, 10) NOT NULL,
    tx_hash       TEXT UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_conversions_wallet_id  ON conversions(wallet_id);
CREATE INDEX idx_conversions_created_at ON conversions(created_at DESC);
