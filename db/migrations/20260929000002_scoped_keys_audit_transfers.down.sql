DROP INDEX IF EXISTS idx_transactions_tags;
DROP INDEX IF EXISTS idx_transactions_tenant_ext_ref;
ALTER TABLE transactions DROP COLUMN IF EXISTS tags;
ALTER TABLE transactions DROP COLUMN IF EXISTS external_reference;

DROP TABLE IF EXISTS tenant_audit_events;

ALTER TABLE api_keys DROP COLUMN IF EXISTS scopes;
