ALTER TABLE tenant_webhook_deliveries DROP COLUMN IF EXISTS signing_key_id;
ALTER TABLE tenant_webhook_configs DROP COLUMN IF EXISTS signing_key_id;
DROP TABLE IF EXISTS tenant_webhook_signing_secrets;
