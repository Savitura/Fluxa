DROP TABLE IF EXISTS refunds;
ALTER TABLE fiat_deposits DROP COLUMN IF EXISTS payment_link_id;
ALTER TABLE fiat_deposits DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE fiat_deposits DROP COLUMN IF EXISTS mode;
DROP TABLE IF EXISTS payment_links;