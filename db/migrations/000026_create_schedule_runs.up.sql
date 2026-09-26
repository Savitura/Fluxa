-- Add missing schedule_status values that exist in the domain layer but were
-- not present in the original migration 000013.
ALTER TYPE schedule_status ADD VALUE IF NOT EXISTS 'processing';
ALTER TYPE schedule_status ADD VALUE IF NOT EXISTS 'failed';

-- ---------------------------------------------------------------------------
-- schedule_run_status enum
-- Possible outcomes for a single scheduled-payout occurrence:
--   pending   – claimed by the worker, payout not yet attempted
--   running   – payout initiation is in progress (or the worker may have
--               crashed before it could update the record)
--   succeeded – payout was successfully initiated and the transaction ID
--               has been recorded
--   failed    – payout initiation failed; error field carries the reason
--   skipped   – the occurrence was intentionally skipped (e.g., schedule
--               was paused or cancelled while the run was pending)
--   cancelled – the schedule was cancelled before this occurrence ran
-- ---------------------------------------------------------------------------
CREATE TYPE schedule_run_status AS ENUM (
    'pending',
    'running',
    'succeeded',
    'failed',
    'skipped',
    'cancelled'
);

-- ---------------------------------------------------------------------------
-- schedule_runs table
-- One row per (schedule_id, expected_run_at) pair.  The UNIQUE constraint is
-- the database-level guard that prevents two concurrent workers from creating
-- duplicate run records for the same occurrence.
-- ---------------------------------------------------------------------------
CREATE TABLE schedule_runs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id     UUID        NOT NULL REFERENCES schedules(id) ON DELETE CASCADE,
    tenant_id       UUID        REFERENCES tenants(id) ON DELETE CASCADE,
    expected_run_at TIMESTAMPTZ NOT NULL,
    status          schedule_run_status NOT NULL DEFAULT 'pending',
    transaction_id  UUID        REFERENCES transactions(id) ON DELETE SET NULL,
    error           TEXT,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Database-enforced uniqueness: one run record per occurrence.
    CONSTRAINT uq_schedule_runs_occurrence UNIQUE (schedule_id, expected_run_at)
);

-- Used by the API: fetch runs for a specific schedule ordered chronologically.
CREATE INDEX idx_schedule_runs_schedule_id_expected
    ON schedule_runs(schedule_id, expected_run_at DESC);

-- Used for tenant-scoped listing queries.
CREATE INDEX idx_schedule_runs_tenant_id
    ON schedule_runs(tenant_id);
