-- Claimable balances Fluxa created on behalf of an org. The row is keyed by
-- the Stellar balance ID, which is derived deterministically from the
-- CreateClaimableBalance operation, so a balance can never be recorded twice
-- and can always be reconciled against Horizon.
CREATE TABLE IF NOT EXISTS claimable_balances (
    id               TEXT PRIMARY KEY,
    org_id           UUID REFERENCES tenants(id) ON DELETE CASCADE,
    asset            VARCHAR(20) NOT NULL,
    amount           NUMERIC(20, 7) NOT NULL,
    claimants        JSONB NOT NULL DEFAULT '[]'::jsonb,
    sponsor          TEXT NOT NULL DEFAULT '',
    status           VARCHAR(20) NOT NULL DEFAULT 'pending',
    revoke_on_expiry BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at       TIMESTAMPTZ,
    claimed_at       TIMESTAMPTZ,
    claimed_by       TEXT NOT NULL DEFAULT ''
);

-- Only the four lifecycle states the service actually writes. 'revoked' is a
-- distinct terminal state from 'expired': it means the funds came back to the
-- org, not that they were abandoned on the ledger.
ALTER TABLE claimable_balances
    ADD CONSTRAINT claimable_balances_status_check
    CHECK (status IN ('pending', 'claimed', 'revoked', 'expired'));

CREATE INDEX IF NOT EXISTS idx_claimable_balances_org_created
    ON claimable_balances(org_id, created_at DESC);

-- Backs the list endpoint's status/asset filters and, crucially, the expiry
-- tracker's "pending and past expires_at" scan, which runs every 5 minutes
-- across every tenant.
CREATE INDEX IF NOT EXISTS idx_claimable_balances_status_expires
    ON claimable_balances(status, expires_at)
    WHERE expires_at IS NOT NULL;

-- Backs the claimant filter. Claimants are a JSONB array, so a GIN index lets
-- `claimants @> '[{"account":"G..."}]'` use an index instead of scanning.
CREATE INDEX IF NOT EXISTS idx_claimable_balances_claimants
    ON claimable_balances USING GIN (claimants jsonb_path_ops);
