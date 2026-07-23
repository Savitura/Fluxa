DROP TABLE IF EXISTS organization_invites;
DROP TABLE IF EXISTS organization_members;
DROP TABLE IF EXISTS users;

ALTER TABLE tenants DROP COLUMN IF EXISTS max_webhooks;
ALTER TABLE tenants DROP COLUMN IF EXISTS max_transfers_per_month;
ALTER TABLE tenants DROP COLUMN IF EXISTS max_wallets;
ALTER TABLE tenants DROP COLUMN IF EXISTS account_type;
