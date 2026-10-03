-- Batch history listing: tenant + created-time keyset index and status filter.
-- The keyset pagination in BatchRepo.List orders by (created_at DESC, id DESC)
-- and filters on tenant_id, so this composite index covers the hot path.
CREATE INDEX IF NOT EXISTS idx_batches_tenant_created_id
    ON batches (tenant_id, created_at DESC, id DESC);

-- Status filter is selective enough on its own for tenants with many batches.
CREATE INDEX IF NOT EXISTS idx_batches_tenant_status
    ON batches (tenant_id, status);
