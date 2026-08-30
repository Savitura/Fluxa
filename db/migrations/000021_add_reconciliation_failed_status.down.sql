-- PostgreSQL does not support removing a value from an enum directly.
-- We recreate the enum without 'reconciliation_failed' and migrate the column.
--
-- The column default must be dropped before the type swap and restored
-- afterwards: PostgreSQL refuses to cast an existing default automatically
-- ("default for column \"status\" cannot be cast automatically").
BEGIN;

ALTER TABLE transactions ALTER COLUMN status DROP DEFAULT;

ALTER TABLE transactions ALTER COLUMN status TYPE TEXT;

ALTER TYPE transaction_status RENAME TO transaction_status_old;

CREATE TYPE transaction_status AS ENUM ('pending', 'submitted', 'confirmed', 'failed');

UPDATE transactions SET status = 'failed' WHERE status = 'reconciliation_failed';

ALTER TABLE transactions ALTER COLUMN status TYPE transaction_status USING status::transaction_status;

ALTER TABLE transactions ALTER COLUMN status SET DEFAULT 'pending';

DROP TYPE transaction_status_old;

COMMIT;
