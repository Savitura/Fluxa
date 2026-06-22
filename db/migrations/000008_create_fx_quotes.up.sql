CREATE TABLE fx_quotes (
    id            UUID PRIMARY KEY,
    org_id        TEXT NOT NULL DEFAULT '',
    from_asset    TEXT NOT NULL,
    to_asset      TEXT NOT NULL,
    from_amount   NUMERIC(20, 7) NOT NULL,
    to_amount     NUMERIC(20, 7) NOT NULL,
    rate          NUMERIC(20, 10) NOT NULL,
    fee           NUMERIC(20, 7) NOT NULL DEFAULT 0,
    expires_at    TIMESTAMPTZ NOT NULL,
    used          BOOLEAN NOT NULL DEFAULT false,
    conversion_id UUID,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_fx_quotes_org_id    ON fx_quotes(org_id);
CREATE INDEX idx_fx_quotes_used      ON fx_quotes(used) WHERE used = false;
CREATE INDEX idx_fx_quotes_created_at ON fx_quotes(created_at DESC);
