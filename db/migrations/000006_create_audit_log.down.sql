DROP TABLE IF EXISTS ledger_audit_log;
DROP TYPE IF EXISTS audit_outcome;

ALTER TABLE transactions
    DROP COLUMN IF EXISTS reconciled_at,
    DROP COLUMN IF EXISTS requeue_count;
