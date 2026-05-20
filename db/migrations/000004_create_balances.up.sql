CREATE TABLE balances (
    wallet_id  UUID        NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    asset_code TEXT        NOT NULL,
    issuer     TEXT        NOT NULL DEFAULT '',
    balance    NUMERIC(20, 7) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (wallet_id, asset_code, issuer)
);

CREATE INDEX idx_balances_wallet_id ON balances(wallet_id);
