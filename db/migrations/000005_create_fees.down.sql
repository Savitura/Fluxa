ALTER TABLE transactions DROP COLUMN IF EXISTS fee_bps;
ALTER TABLE transactions DROP COLUMN IF EXISTS tenant_id;

ALTER TABLE conversions DROP COLUMN IF EXISTS fee_bps;
ALTER TABLE conversions DROP COLUMN IF EXISTS fee_amount;

DROP TABLE IF EXISTS fee_collections;
DROP TABLE IF EXISTS fees;
