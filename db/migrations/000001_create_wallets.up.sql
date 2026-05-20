CREATE TABLE wallets (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_key       TEXT NOT NULL UNIQUE,
    encrypted_secret TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wallets_public_key ON wallets(public_key);
