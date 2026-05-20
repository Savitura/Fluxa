CREATE TYPE transaction_status AS ENUM ('pending', 'submitted', 'confirmed', 'failed');
CREATE TYPE transaction_type   AS ENUM ('transfer', 'conversion', 'funding');

CREATE TABLE transactions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tx_hash     TEXT UNIQUE,
    type        transaction_type   NOT NULL,
    status      transaction_status NOT NULL DEFAULT 'pending',
    from_wallet UUID REFERENCES wallets(id) ON DELETE SET NULL,
    to_wallet   UUID REFERENCES wallets(id) ON DELETE SET NULL,
    asset       TEXT NOT NULL,
    amount      NUMERIC(20, 7) NOT NULL,
    fee         NUMERIC(20, 7),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_transactions_from_wallet ON transactions(from_wallet);
CREATE INDEX idx_transactions_to_wallet   ON transactions(to_wallet);
CREATE INDEX idx_transactions_status      ON transactions(status);
CREATE INDEX idx_transactions_created_at  ON transactions(created_at DESC);
