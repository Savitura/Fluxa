ALTER TABLE batch_transfers
    DROP COLUMN settled_at,
    DROP COLUMN failed_count,
    DROP COLUMN succeeded_count;

ALTER TABLE batch_transfers
    RENAME CONSTRAINT batch_transfers_mode_check TO batches_mode_check;

ALTER INDEX idx_batch_transfers_tenant_id RENAME TO idx_batches_tenant_id;
ALTER INDEX idx_batch_transfers_tenant_mode RENAME TO idx_batches_tenant_mode;

ALTER TABLE batch_transfers RENAME TO batches;
