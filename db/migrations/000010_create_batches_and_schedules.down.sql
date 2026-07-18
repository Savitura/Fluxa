DROP TABLE IF EXISTS schedules;
DROP TYPE IF EXISTS schedule_status;
DROP TYPE IF EXISTS schedule_frequency;

DROP INDEX IF EXISTS idx_transactions_batch_id;
ALTER TABLE transactions DROP COLUMN IF EXISTS reference;
ALTER TABLE transactions DROP COLUMN IF EXISTS batch_id;

DROP TABLE IF EXISTS batches;
DROP TYPE IF EXISTS batch_status;
