DROP TABLE IF EXISTS audit_log;

ALTER TABLE transactions DROP COLUMN IF EXISTS local_amount;
ALTER TABLE transactions DROP COLUMN IF EXISTS local_currency;
ALTER TABLE transactions DROP COLUMN IF EXISTS fiat_status;
ALTER TABLE transactions DROP COLUMN IF EXISTS fiat_provider_ref;
ALTER TABLE transactions DROP COLUMN IF EXISTS fiat_rail;

ALTER TABLE fiat_deposits DROP COLUMN IF EXISTS instructions;
