CREATE TABLE revoked_refresh_tokens (
    token_hash BYTEA PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_revoked_refresh_tokens_expires_at ON revoked_refresh_tokens(expires_at);
