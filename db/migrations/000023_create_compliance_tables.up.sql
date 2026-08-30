-- Compliance review queue: one row per transfer placed on compliance_hold.
CREATE TABLE IF NOT EXISTS compliance_reviews (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    org_id         UUID,
    status         VARCHAR(20) NOT NULL DEFAULT 'pending',
    risk_score     INT NOT NULL DEFAULT 0,
    rules_fired    TEXT[] NOT NULL DEFAULT '{}',
    reason         TEXT NOT NULL DEFAULT '',
    reviewed_by    UUID,
    review_notes   TEXT NOT NULL DEFAULT '',
    reviewed_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One open review per transaction; approve/reject is a terminal state change,
-- never a second row, so the hold/approve audit trail cannot fork.
CREATE UNIQUE INDEX IF NOT EXISTS idx_compliance_reviews_transaction
    ON compliance_reviews(transaction_id);
CREATE INDEX IF NOT EXISTS idx_compliance_reviews_status_created
    ON compliance_reviews(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_compliance_reviews_org
    ON compliance_reviews(org_id);

-- Blocked transfers never become transactions, so this table is the only
-- record that the attempt happened. Deliberately has no transaction_id.
CREATE TABLE IF NOT EXISTS compliance_blocks (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID,
    from_wallet_id UUID,
    to_wallet_id   UUID,
    to_address     TEXT NOT NULL DEFAULT '',
    asset          VARCHAR(12) NOT NULL DEFAULT '',
    amount         DECIMAL(30,7),
    rules_fired    TEXT[] NOT NULL DEFAULT '{}',
    reason         TEXT NOT NULL DEFAULT '',
    matched_entity TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_compliance_blocks_org_created
    ON compliance_blocks(org_id, created_at DESC);

-- Audit trail of every OFAC SDN refresh attempt, successful or not.
CREATE TABLE IF NOT EXISTS sanctions_list_updates (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source       VARCHAR(50) NOT NULL DEFAULT 'ofac_sdn',
    status       VARCHAR(20) NOT NULL,
    entity_count INT NOT NULL DEFAULT 0,
    duration_ms  BIGINT NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT '',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_sanctions_list_updates_finished
    ON sanctions_list_updates(finished_at DESC);

-- Parsed SDN entries. The worker refreshes this table once a day; every
-- process reloads its own in-memory set from here on a ticker, so a refresh
-- performed by cmd/worker still reaches cmd/api's screener.
CREATE TABLE IF NOT EXISTS sanctions_entities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    uid          TEXT NOT NULL,
    name         TEXT NOT NULL,
    entity_type  VARCHAR(50) NOT NULL DEFAULT '',
    address      TEXT NOT NULL DEFAULT '',
    address_type VARCHAR(80) NOT NULL DEFAULT '',
    programs     TEXT[] NOT NULL DEFAULT '{}',
    source       VARCHAR(50) NOT NULL DEFAULT 'ofac_sdn',
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Upsert key. A single SDN entry yields one row per digital-currency address
-- plus one row per name/aka, so (uid, name, address) is what is unique.
CREATE UNIQUE INDEX IF NOT EXISTS idx_sanctions_entities_uid_name_address
    ON sanctions_entities(uid, name, address);
CREATE INDEX IF NOT EXISTS idx_sanctions_entities_address
    ON sanctions_entities(address) WHERE address <> '';

-- Screening runs three velocity queries on the hot transfer path; these back
-- them. idx_transactions_tenant_id and idx_transactions_to_wallet already
-- exist but are not selective enough on their own for the time windows.
CREATE INDEX IF NOT EXISTS idx_transactions_tenant_created
    ON transactions(tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_to_wallet_created
    ON transactions(to_wallet, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_from_wallet_created
    ON transactions(from_wallet, created_at DESC);
