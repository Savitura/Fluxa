CREATE TABLE IF NOT EXISTS dependency_health_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dependency VARCHAR(64) NOT NULL CHECK (dependency ~ '^[a-z][a-z0-9_-]{0,63}$'),
    status VARCHAR(16) NOT NULL CHECK (status IN ('healthy', 'unhealthy')),
    latency_ms BIGINT NOT NULL CHECK (latency_ms >= 0),
    checked_at TIMESTAMPTZ NOT NULL,
    UNIQUE (dependency, checked_at)
);

CREATE INDEX idx_dependency_health_checked_at
    ON dependency_health_checks (checked_at DESC, dependency);
CREATE INDEX idx_dependency_health_dependency_checked_at
    ON dependency_health_checks (dependency, checked_at DESC);