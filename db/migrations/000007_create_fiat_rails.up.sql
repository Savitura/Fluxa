CREATE TABLE fiat_deposits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    provider VARCHAR(50) NOT NULL,
    provider_reference VARCHAR(255) UNIQUE NOT NULL,
    fiat_amount DECIMAL(20,4) NOT NULL,
    fiat_currency VARCHAR(10) NOT NULL,
    usdc_amount DECIMAL(20,4) NOT NULL,
    status VARCHAR(50) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fiat_deposits_wallet_id ON fiat_deposits(wallet_id);
CREATE INDEX idx_fiat_deposits_provider_ref ON fiat_deposits(provider, provider_reference);

CREATE TABLE fiat_withdrawals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id UUID NOT NULL REFERENCES wallets(id),
    provider VARCHAR(50) NOT NULL,
    provider_reference VARCHAR(255) UNIQUE NOT NULL,
    fiat_amount DECIMAL(20,4) NOT NULL,
    fiat_currency VARCHAR(10) NOT NULL,
    usdc_amount DECIMAL(20,4) NOT NULL,
    status VARCHAR(50) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fiat_withdrawals_wallet_id ON fiat_withdrawals(wallet_id);
CREATE INDEX idx_fiat_withdrawals_provider_ref ON fiat_withdrawals(provider, provider_reference);
