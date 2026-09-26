ALTER TABLE transactions DROP COLUMN tenant_id;
ALTER TABLE wallets DROP COLUMN tenant_id;

DROP TABLE api_keys;
DROP TABLE tenants;
