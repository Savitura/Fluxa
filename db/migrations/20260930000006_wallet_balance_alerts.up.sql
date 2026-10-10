-- Wallet Balance Alerts table
CREATE TABLE IF NOT EXISTS wallet_balance_alerts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    mode VARCHAR(20) NOT NULL DEFAULT 'live' CHECK (mode IN ('live', 'test')),
    asset_code VARCHAR(12) NOT NULL,
    asset_issuer VARCHAR(56),
    threshold DECIMAL(38, 18) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, mode, wallet_id, asset_code, COALESCE(asset_issuer, ''))
);

CREATE INDEX idx_wallet_balance_alerts_tenant ON wallet_balance_alerts(tenant_id);
CREATE INDEX idx_wallet_balance_alerts_wallet ON wallet_balance_alerts(wallet_id);
CREATE INDEX idx_wallet_balance_alerts_status ON wallet_balance_alerts(status);

-- Wallet Balance Alert Events table
CREATE TABLE IF NOT EXISTS wallet_balance_alert_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_id UUID NOT NULL REFERENCES wallet_balance_alerts(id) ON DELETE CASCADE,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    asset_code VARCHAR(12) NOT NULL,
    asset_issuer VARCHAR(56),
    balance DECIMAL(38, 18) NOT NULL,
    threshold DECIMAL(38, 18) NOT NULL,
    triggered_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wallet_balance_alert_events_alert ON wallet_balance_alert_events(alert_id);
CREATE INDEX idx_wallet_balance_alert_events_wallet ON wallet_balance_alert_events(wallet_id);
CREATE INDEX idx_wallet_balance_alert_events_triggered_at ON wallet_balance_alert_events(triggered_at);
