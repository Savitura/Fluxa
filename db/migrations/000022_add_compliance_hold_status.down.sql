-- PostgreSQL does not support removing a value from an enum directly.
-- We recreate the enum without 'compliance_hold' and migrate the column.
-- Held transfers were never submitted to Stellar, so they collapse to
-- 'failed' rather than 'pending' — reverting must not release a payment
-- that a compliance officer had deliberately stopped.
--
-- The column default must be dropped before the type swap and restored
-- afterwards: PostgreSQL refuses to cast an existing default automatically
-- ("default for column \"status\" cannot be cast automatically").
BEGIN;

ALTER TABLE transactions ALTER COLUMN status DROP DEFAULT;

ALTER TABLE transactions ALTER COLUMN status TYPE TEXT;

ALTER TYPE transaction_status RENAME TO transaction_status_old;

CREATE TYPE transaction_status AS ENUM ('pending', 'submitted', 'confirmed', 'failed', 'reconciliation_failed');

UPDATE transactions SET status = 'failed' WHERE status = 'compliance_hold';

ALTER TABLE transactions ALTER COLUMN status TYPE transaction_status USING status::transaction_status;

ALTER TABLE transactions ALTER COLUMN status SET DEFAULT 'pending';

DROP TYPE transaction_status_old;

COMMIT;
