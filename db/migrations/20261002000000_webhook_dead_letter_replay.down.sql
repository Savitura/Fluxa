DROP TABLE IF EXISTS webhook_delivery_attempts;

ALTER TABLE webhook_deliveries
    DROP COLUMN IF EXISTS replay_of;

DROP INDEX IF EXISTS idx_webhook_dead_letters_replay_token;
DROP INDEX IF EXISTS idx_webhook_dead_letters_retention;
DROP INDEX IF EXISTS idx_webhook_dead_letters_event;
DROP INDEX IF EXISTS idx_webhook_dead_letters_status;

ALTER TABLE webhook_dead_letters
    DROP COLUMN IF EXISTS retain_until,
    DROP COLUMN IF EXISTS redacted_fields,
    DROP COLUMN IF EXISTS replay_delivery_id,
    DROP COLUMN IF EXISTS replay_token,
    DROP COLUMN IF EXISTS last_replayed_at,
    DROP COLUMN IF EXISTS replay_count,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS event_type;
