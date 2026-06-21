CREATE TABLE reconciliation_runs (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    started_at           TIMESTAMPTZ NOT NULL,
    completed_at         TIMESTAMPTZ NOT NULL,
    txs_checked          INT NOT NULL DEFAULT 0,
    discrepancies_found  INT NOT NULL DEFAULT 0,
    corrections_made     INT NOT NULL DEFAULT 0
);

CREATE INDEX idx_reconciliation_runs_started_at ON reconciliation_runs(started_at DESC);
