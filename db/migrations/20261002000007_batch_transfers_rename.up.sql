-- #21 renames the existing partial batch table to the name the issue documents
-- (batch_transfers) and adds the per-transfer outcome aggregates the batch
-- summary endpoints report. transactions.batch_id keeps its foreign key: a
-- table rename is followed by the referencing constraint automatically, so no
-- data is touched.

ALTER TABLE batches RENAME TO batch_transfers;

ALTER INDEX idx_batches_tenant_id RENAME TO idx_batch_transfers_tenant_id;
ALTER INDEX idx_batches_tenant_mode RENAME TO idx_batch_transfers_tenant_mode;

ALTER TABLE batch_transfers
    RENAME CONSTRAINT batches_mode_check TO batch_transfers_mode_check;

ALTER TABLE batch_transfers
    ADD COLUMN succeeded_count INT NOT NULL DEFAULT 0,
    ADD COLUMN failed_count    INT NOT NULL DEFAULT 0,
    ADD COLUMN settled_at      TIMESTAMPTZ;
