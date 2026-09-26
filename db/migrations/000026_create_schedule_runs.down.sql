-- Drop the schedule_runs table and its enum type.
-- schedule_status enum values cannot be removed in PostgreSQL without
-- recreating the type; we leave them in place on rollback to avoid
-- disrupting existing schedule rows.
DROP TABLE IF EXISTS schedule_runs;
DROP TYPE IF EXISTS schedule_run_status;
