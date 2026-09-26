DROP TABLE IF EXISTS webhook_dead_letters;

ALTER TABLE webhook_deliveries
    DROP COLUMN IF NOT EXISTS tenant_id,
    DROP COLUMN IF NOT EXISTS response_body,
    DROP COLUMN IF NOT EXISTS max_attempts,
    DROP COLUMN IF NOT EXISTS next_attempt_at,
    DROP COLUMN IF NOT EXISTS last_attempt;

ALTER TABLE webhook_endpoints
    DROP COLUMN IF NOT EXISTS success_count,
    DROP COLUMN IF NOT EXISTS failure_count,
    DROP COLUMN IF NOT EXISTS last_delivered_at,
    DROP COLUMN IF NOT EXISTS notified_failing;
