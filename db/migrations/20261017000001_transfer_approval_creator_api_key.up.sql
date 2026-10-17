-- API keys have no owning user, so transfers created with a key previously
-- stored creator_id as NULL and lost who created them. Record the key instead.
ALTER TABLE transfer_approval_requests
    ADD COLUMN creator_api_key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
